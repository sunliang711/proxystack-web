package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"proxystack-web/internal/audit"
	"proxystack-web/internal/deps"
	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/subpublish"
)

const (
	subDeployTimeout   = 15 * time.Minute
	subRollbackTimeout = 5 * time.Minute
)

// SubDeployRequest selects the release installed on a subscription target. Empty means latest stable.
type SubDeployRequest struct {
	Version string `json:"version"`
}

func (s *Service) reserveSubDeploy(id int64) error {
	return s.reserveSubTargets([]int64{id})
}

func (s *Service) reserveSubTargets(ids []int64) error {
	s.subDeployMu.Lock()
	defer s.subDeployMu.Unlock()
	if s.subDeploying == nil {
		s.subDeploying = map[int64]bool{}
	}
	for _, id := range ids {
		if s.subDeploying[id] {
			return errors.New("a publish or deployment operation is already running for this subscription target")
		}
	}
	for _, id := range ids {
		s.subDeploying[id] = true
	}
	return nil
}

func (s *Service) releaseSubDeploy(id int64) {
	s.releaseSubTargets([]int64{id})
}

func (s *Service) releaseSubTargets(ids []int64) {
	s.subDeployMu.Lock()
	for _, id := range ids {
		delete(s.subDeploying, id)
	}
	s.subDeployMu.Unlock()
}

func (s *Service) lockSubTargetMutation(id int64) (func(), error) {
	s.subDeployMu.Lock()
	if s.subDeploying[id] {
		s.subDeployMu.Unlock()
		return nil, errors.New("the subscription target cannot be changed while a publish or deployment operation is running")
	}
	return s.subDeployMu.Unlock, nil
}

// InspectPublishTargetDeploy checks platform, sudo, installation state, and installed version through SSH.
func (s *Service) InspectPublishTargetDeploy(ctx context.Context, actor Actor, id int64) (*subpublish.DeployStatus, error) {
	identity, target, err := s.publishTargetState(ctx, id)
	if err != nil {
		return nil, err
	}
	status, inspectErr := s.SubDeployer.InspectDeploy(ctx, subpublish.Identity{Seed: identity.Seed}, clientTarget(target))
	errorText := ""
	if inspectErr != nil {
		inspectErr = &RemoteError{Message: inspectErr.Error()}
		errorText = inspectErr.Error()
	}
	updateErr := s.updateDeployTargetState(context.WithoutCancel(ctx), target, status, errorText)
	if updateErr != nil {
		return status, updateErr
	}
	_ = s.Store.InTx(context.WithoutCancel(ctx), func(q *dbq.Queries) error {
		return audit.Write(context.WithoutCancel(ctx), q, s.Now(), audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionSubDeployInspect, Object: fmt.Sprint(id),
			Summary: fmt.Sprintf("检测订阅端部署状态 %s（成功=%t）", target.Name, inspectErr == nil),
		})
	})
	return status, inspectErr
}

// DeployPublishTarget downloads a verified release and applies it through SSH in a background task.
func (s *Service) DeployPublishTarget(ctx context.Context, actor Actor, id int64, request SubDeployRequest) (int64, error) {
	if err := s.reserveSubDeploy(id); err != nil {
		return 0, err
	}
	identity, target, err := s.publishTargetState(ctx, id)
	if err != nil {
		s.releaseSubDeploy(id)
		return 0, err
	}
	taskID, err := s.startTask("subscription-deploy", "部署订阅端 "+target.Name, actor.Name, func(ctx context.Context, logf Logf) (runErr error) {
		defer s.releaseSubDeploy(id)
		ctx, cancel := context.WithTimeout(ctx, subDeployTimeout)
		defer cancel()
		defer func() {
			if runErr != nil {
				_ = s.updateDeployTargetState(context.Background(), target, nil, runErr.Error())
			}
		}()
		logf("Inspecting remote platform and sudo access")
		status, err := s.SubDeployer.InspectDeploy(ctx, subpublish.Identity{Seed: identity.Seed}, clientTarget(target))
		if err != nil {
			return err
		}
		logf("Remote platform: %s/%s; deployment state: %s", status.OS, status.Arch, status.State)
		doc, err := s.LoadConfig(ctx)
		if err != nil {
			return err
		}
		fetcher, proxy, err := s.fetcher(ctx, doc, doc.Settings.DownloadProxy)
		if err != nil {
			return err
		}
		defer fetcher.CloseIdleConnections()
		logf("Download proxy: %s", proxy.Redacted())
		release, err := deps.FindRelease(ctx, fetcher, deps.Self, request.Version)
		if err != nil {
			return err
		}
		if subDeployCurrent(status, release.Tag, request.Version == "") {
			logf("Subscription server %s is already up to date with %s; skipping binary download", status.Version, release.Tag)
			logf("Configuring the subscription publish token")
			if err := s.SubDeployer.ConfigurePublishToken(ctx, subpublish.Identity{Seed: identity.Seed}, clientTarget(target), target.Source, target.Secret); err != nil {
				return err
			}
			if err := s.updateDeployTargetState(ctx, target, status, ""); err != nil {
				return err
			}
			_ = s.Store.InTx(ctx, func(q *dbq.Queries) error {
				return audit.Write(ctx, q, s.Now(), audit.Entry{
					Actor: actor.Name, SourceIP: actor.IP, Action: ActionSubDeployApply, Object: fmt.Sprint(id),
					Summary: fmt.Sprintf("部署订阅端 %s：unchanged（已是 %s）", target.Name, status.Version),
				})
			})
			logf("Subscription server unchanged: %s", status.Version)
			return nil
		}
		binary, expected, err := downloadSubBinary(ctx, logf, fetcher, release, status.Arch)
		if err != nil {
			return err
		}
		_, publicKey, err := s.SubscriptionIdentity(ctx)
		if err != nil {
			return err
		}
		result, err := s.SubDeployer.Deploy(ctx, subpublish.Identity{Seed: identity.Seed}, clientTarget(target), subpublish.DeployRequest{
			Binary: binary, SHA256: expected, Source: target.Source, PublicKey: publicKey, Port: target.RemotePort, Secret: target.Secret,
			Logf: subpublish.Logf(logf),
		})
		if err != nil {
			return err
		}
		updated := &subpublish.DeployStatus{
			OS: "linux", Arch: status.Arch, Sudo: true, State: "installed", Version: result.Version,
			PreviousVersion: result.PreviousVersion, ServiceActive: true, ServiceEnabled: true,
		}
		if err := s.updateDeployTargetState(ctx, target, updated, ""); err != nil {
			return err
		}
		_ = s.Store.InTx(ctx, func(q *dbq.Queries) error {
			return audit.Write(ctx, q, s.Now(), audit.Entry{
				Actor: actor.Name, SourceIP: actor.IP, Action: ActionSubDeployApply, Object: fmt.Sprint(id),
				Summary: fmt.Sprintf("部署订阅端 %s：%s", target.Name, result.Action),
			})
		})
		logf("Subscription server %s: %s", result.Action, result.Version)
		return nil
	})
	if err != nil {
		s.releaseSubDeploy(id)
		return 0, err
	}
	return taskID, nil
}

// RollbackPublishTarget rolls a subscription target back through SSH in a background task.
func (s *Service) RollbackPublishTarget(ctx context.Context, actor Actor, id int64) (int64, error) {
	if err := s.reserveSubDeploy(id); err != nil {
		return 0, err
	}
	identity, target, err := s.publishTargetState(ctx, id)
	if err != nil {
		s.releaseSubDeploy(id)
		return 0, err
	}
	taskID, err := s.startTask("subscription-deploy", "回滚订阅端 "+target.Name, actor.Name, func(ctx context.Context, logf Logf) (runErr error) {
		defer s.releaseSubDeploy(id)
		ctx, cancel := context.WithTimeout(ctx, subRollbackTimeout)
		defer cancel()
		defer func() {
			if runErr != nil {
				_ = s.updateDeployTargetState(context.Background(), target, nil, runErr.Error())
			}
		}()
		logf("Rolling back the subscription server")
		result, err := s.SubDeployer.RollbackDeploy(ctx, subpublish.Identity{Seed: identity.Seed}, clientTarget(target))
		if err != nil {
			return err
		}
		status := &subpublish.DeployStatus{State: "installed", Version: result.Version, PreviousVersion: result.PreviousVersion, ServiceActive: true, ServiceEnabled: true}
		if err := s.updateDeployTargetState(ctx, target, status, ""); err != nil {
			return err
		}
		_ = s.Store.InTx(ctx, func(q *dbq.Queries) error {
			return audit.Write(ctx, q, s.Now(), audit.Entry{Actor: actor.Name, SourceIP: actor.IP, Action: ActionSubDeployRollback, Object: fmt.Sprint(id), Summary: "回滚订阅端 " + target.Name})
		})
		logf("Subscription server rolled back to %s", result.Version)
		return nil
	})
	if err != nil {
		s.releaseSubDeploy(id)
		return 0, err
	}
	return taskID, nil
}

// downloadSubBinary downloads the Linux program of release for arch and verifies it against the published SHA-256.
func downloadSubBinary(ctx context.Context, logf Logf, fetcher *deps.HTTPFetcher, release deps.Release, arch string) ([]byte, string, error) {
	asset, digest, err := deps.SelectAsset(deps.Self, release, "linux", arch, "")
	if err != nil {
		return nil, "", err
	}
	if digest == nil {
		return nil, "", fmt.Errorf("release %s has no SHA-256 file for %s", release.Tag, asset.Name)
	}
	logf("Downloading %s for linux/%s", release.Tag, arch)
	started := time.Now()
	binary, err := fetcher.Download(ctx, asset.URL, nil)
	if err != nil {
		return nil, "", err
	}
	elapsed := time.Since(started).Round(100 * time.Millisecond)
	logf("Downloaded %.1f MB in %s", float64(len(binary))/(1<<20), elapsed)
	digestData, err := fetcher.Get(ctx, digest.URL)
	if err != nil {
		return nil, "", err
	}
	expected, err := deps.ParseDigest(string(digestData))
	if err != nil {
		return nil, "", err
	}
	if err := deps.Verify(binary, expected); err != nil {
		return nil, "", err
	}
	logf("SHA-256 verification succeeded")
	return binary, expected, nil
}

// SubUninstallRequest controls whether the target configuration is deleted after a successful uninstall.
type SubUninstallRequest struct {
	DeleteTarget bool `json:"delete_target" doc:"卸载成功后删除管理端保存的这个订阅端"`
}

// sharedSubHost returns another target on the same SSH host and port. Targets on one host share one installation
// directory, so uninstalling for one of them removes the service for all.
func sharedSubHost(targets []publishTargetState, target publishTargetState) string {
	for _, other := range targets {
		if other.ID != target.ID && strings.EqualFold(other.SSHHost, target.SSHHost) && other.SSHPort == target.SSHPort {
			return other.Name
		}
	}
	return ""
}

// UninstallPublishTarget removes the subscription service, its configuration, imported bundles and service account
// through SSH in a background task. Subscription data is not kept because it can be published again after reinstalling.
// When the program was already removed, a verified release is uploaded to run the removal. The SSH key, sudoers rule
// and sshd restriction stay on the host and are cleaned up by hand.
func (s *Service) UninstallPublishTarget(ctx context.Context, actor Actor, id int64, request SubUninstallRequest) (int64, error) {
	var targets []publishTargetState
	if err := s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		targets, err = loadPublishTargets(ctx, q)
		return err
	}); err != nil {
		return 0, err
	}
	if err := s.reserveSubDeploy(id); err != nil {
		return 0, err
	}
	identity, target, err := s.publishTargetState(ctx, id)
	if err != nil {
		s.releaseSubDeploy(id)
		return 0, err
	}
	if other := sharedSubHost(targets, target); other != "" {
		s.releaseSubDeploy(id)
		return 0, &FieldError{Field: "id", Message: fmt.Sprintf("订阅端 %s 与 %s 位于同一台主机，共用同一个安装目录，卸载会同时影响它；请先删除其中一个订阅端，或在主机上手动卸载", target.Name, other)}
	}
	taskID, err := s.startTask("subscription-deploy", "卸载订阅端 "+target.Name, actor.Name, func(ctx context.Context, logf Logf) (runErr error) {
		released := false
		release := func() {
			if !released {
				released = true
				s.releaseSubDeploy(id)
			}
		}
		defer release()
		ctx, cancel := context.WithTimeout(ctx, subDeployTimeout)
		defer cancel()
		defer func() {
			if runErr != nil {
				_ = s.updateDeployTargetState(context.Background(), target, nil, runErr.Error())
			}
		}()
		logf("Inspecting remote platform and sudo access")
		sshIdentity := subpublish.Identity{Seed: identity.Seed}
		status, err := s.SubDeployer.InspectDeploy(ctx, sshIdentity, clientTarget(target))
		if err != nil {
			return err
		}
		logf("Remote platform: %s/%s; deployment state: %s", status.OS, status.Arch, status.State)
		action := "nothing to remove"
		switch status.State {
		case "uninstalled":
			logf("The subscription server is not installed; nothing to remove")
		case "installed":
			logf("Removing the subscription service, configuration, imported bundles and service account")
			result, err := s.SubDeployer.UninstallDeploy(ctx, sshIdentity, clientTarget(target), nil, subpublish.Logf(logf))
			if err != nil {
				return err
			}
			action = result.Action
		default:
			logf("The program was already removed but data remains; downloading a release to remove it")
			doc, err := s.LoadConfig(ctx)
			if err != nil {
				return err
			}
			fetcher, proxy, err := s.fetcher(ctx, doc, doc.Settings.DownloadProxy)
			if err != nil {
				return err
			}
			defer fetcher.CloseIdleConnections()
			logf("Download proxy: %s", proxy.Redacted())
			found, err := deps.FindRelease(ctx, fetcher, deps.Self, "")
			if err != nil {
				return err
			}
			binary, _, err := downloadSubBinary(ctx, logf, fetcher, found, status.Arch)
			if err != nil {
				return err
			}
			logf("Removing the remaining configuration, imported bundles and service account")
			result, err := s.SubDeployer.UninstallDeploy(ctx, sshIdentity, clientTarget(target), binary, subpublish.Logf(logf))
			if err != nil {
				return err
			}
			action = result.Action
		}
		removed := &subpublish.DeployStatus{OS: status.OS, Arch: status.Arch, Sudo: status.Sudo, State: "uninstalled"}
		if err := s.updateDeployTargetState(ctx, target, removed, ""); err != nil {
			return err
		}
		_ = s.Store.InTx(ctx, func(q *dbq.Queries) error {
			return audit.Write(ctx, q, s.Now(), audit.Entry{
				Actor: actor.Name, SourceIP: actor.IP, Action: ActionSubDeployUninstall, Object: fmt.Sprint(id),
				Summary: fmt.Sprintf("卸载订阅端 %s：%s", target.Name, action),
			})
		})
		logf("Subscription server removed (%s)", action)
		if request.DeleteTarget {
			release()
			if err := s.DeletePublishTarget(ctx, actor, id); err != nil {
				return fmt.Errorf("the subscription server was removed, but deleting the target failed: %w", err)
			}
			logf("Deleted the subscription target %s from the panel", target.Name)
		}
		logf("The SSH key, sudoers rule and sshd restriction remain on the host; remove them by hand when no other target uses them")
		return nil
	})
	if err != nil {
		s.releaseSubDeploy(id)
		return 0, err
	}
	return taskID, nil
}

// subDeployCurrent reports whether a running installation already provides the requested release.
// For the latest stable request, a comparable installed version newer than the release also counts.
func subDeployCurrent(status *subpublish.DeployStatus, tag string, latest bool) bool {
	if status.State != "installed" || !status.ServiceActive || status.Version == "" {
		return false
	}
	installed, release := strings.TrimPrefix(status.Version, "v"), strings.TrimPrefix(tag, "v")
	return installed == release || latest && deps.IsNewerVersion(tag, status.Version)
}

func (s *Service) updateDeployTargetState(ctx context.Context, snapshot publishTargetState, status *subpublish.DeployStatus, errorText string) error {
	return s.Store.InTx(ctx, func(q *dbq.Queries) error {
		targets, err := loadPublishTargets(ctx, q)
		if err != nil {
			return err
		}
		updated := false
		for index := range targets {
			if targets[index].ID != snapshot.ID || !samePublishTargetConfig(targets[index], snapshot) {
				continue
			}
			targets[index].LastDeployAt = s.Now().Unix()
			targets[index].LastDeployError = strings.TrimSpace(errorText)
			if status != nil {
				targets[index].DeployState = status.State
				targets[index].CurrentVersion = status.Version
				targets[index].PreviousVersion = status.PreviousVersion
			}
			updated = true
		}
		if !updated {
			return nil
		}
		return store.PutJSON(ctx, q, settingSubPublishTargets, targets)
	})
}
