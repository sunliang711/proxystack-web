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
		asset, digest, err := deps.SelectAsset(deps.Self, release, "linux", status.Arch, "")
		if err != nil {
			return err
		}
		if digest == nil {
			return fmt.Errorf("release %s has no SHA-256 file for %s", release.Tag, asset.Name)
		}
		logf("Downloading %s for linux/%s", release.Tag, status.Arch)
		binary, err := fetcher.Download(ctx, asset.URL, nil)
		if err != nil {
			return err
		}
		digestData, err := fetcher.Get(ctx, digest.URL)
		if err != nil {
			return err
		}
		expected, err := deps.ParseDigest(string(digestData))
		if err != nil {
			return err
		}
		if err := deps.Verify(binary, expected); err != nil {
			return err
		}
		logf("SHA-256 verification succeeded")
		_, publicKey, err := s.SubscriptionIdentity(ctx)
		if err != nil {
			return err
		}
		logf("Uploading and applying the subscription server binary")
		result, err := s.SubDeployer.Deploy(ctx, subpublish.Identity{Seed: identity.Seed}, clientTarget(target), subpublish.DeployRequest{
			Binary: binary, SHA256: expected, Source: target.Source, PublicKey: publicKey, Port: target.RemotePort, Secret: target.Secret,
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
