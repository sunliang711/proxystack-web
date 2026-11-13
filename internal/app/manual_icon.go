package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"proxystack-web/internal/access"
	"proxystack-web/internal/audit"
	"proxystack-web/internal/favicon"
	"proxystack-web/internal/store/dbq"
)

// 手动图标挂在主域名或服务上。
const (
	IconKindDomain  = "domain"
	IconKindService = "service"
)

// 手动图标的审计动作。
const (
	ActionIconSet    = "access.icon_set"
	ActionIconDelete = "access.icon_delete"
)

// maxIconServiceName 是服务名的最大长度，与手动归类的服务名一致。
const maxIconServiceName = 40

// IconRef 指定一个手动图标。
type IconRef struct {
	Kind string `json:"kind" enum:"domain,service"`
	Name string `json:"name" minLength:"1" maxLength:"253"`
}

// ManualIcon 是一个手动设置的图标（不含图片内容）。
type ManualIcon struct {
	Kind        string    `json:"kind" enum:"domain,service"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	UpdatedAt   time.Time `json:"updated_at"`
	UpdatedBy   string    `json:"updated_by"`
}

// normalizeIconRef 规范化图标的挂载对象：主域名先归并（www.example.com 归为 example.com），服务名转为小写。
func normalizeIconRef(ref IconRef) (IconRef, error) {
	switch ref.Kind {
	case IconKindDomain:
		// 访问日志中的国际化域名是 xn-- 形式，这里也要求这样写；只允许域名字符，example.com:443 这类写法永远匹配不到。
		name := access.Domain(ref.Name)
		if name == "" || strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789.-_") != "" {
			return ref, &FieldError{Field: "name", Message: "must be a domain name; write internationalized domains in xn-- form"}
		}
		return IconRef{Kind: ref.Kind, Name: name}, nil
	case IconKindService:
		name := strings.ToLower(strings.TrimSpace(ref.Name))
		if name == "" || utf8.RuneCountInString(name) > maxIconServiceName || strings.ContainsAny(name, "=\r\n\t") {
			return ref, &FieldError{Field: "name", Message: fmt.Sprintf("service must be 1 to %d characters", maxIconServiceName)}
		}
		return IconRef{Kind: ref.Kind, Name: name}, nil
	}
	return ref, &FieldError{Field: "kind", Message: "must be domain or service"}
}

func manualIconLabel(ref IconRef) string {
	if ref.Kind == IconKindService {
		return "服务 " + ref.Name
	}
	return ref.Name
}

// manualIcon 读取手动图标，没有时返回 nil。
func (s *Service) manualIcon(ctx context.Context, ref IconRef) (*favicon.Icon, error) {
	var icon *favicon.Icon
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		row, err := q.GetManualIcon(ctx, dbq.GetManualIconParams{Kind: ref.Kind, Name: ref.Name})
		if err == nil {
			icon = &favicon.Icon{ContentType: row.ContentType, Data: row.Data}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return nil
	})
	return icon, err
}

// AccessServiceIcon 返回服务的手动图标；服务没有手动图标时返回 ErrNotFound，页面改用代表域名的图标。
func (s *Service) AccessServiceIcon(ctx context.Context, service string) (*favicon.Icon, error) {
	ref, err := normalizeIconRef(IconRef{Kind: IconKindService, Name: service})
	if err != nil {
		return nil, ErrNotFound
	}
	icon, err := s.manualIcon(ctx, ref)
	if err != nil {
		return nil, err
	}
	if icon == nil {
		return nil, ErrNotFound
	}
	return icon, nil
}

// ListManualIcons 列出全部手动图标。
func (s *Service) ListManualIcons(ctx context.Context) ([]ManualIcon, error) {
	out := []ManualIcon{}
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		rows, err := q.ListManualIcons(ctx)
		for _, row := range rows {
			out = append(out, ManualIcon{
				Kind: row.Kind, Name: row.Name, ContentType: row.ContentType, Size: row.Size.Int64,
				UpdatedAt: time.Unix(row.UpdatedAt, 0), UpdatedBy: row.UpdatedBy,
			})
		}
		return err
	})
	return out, err
}

// SetManualIcon 上传手动图标：接受 PNG、GIF、JPEG、WebP、ICO 和 SVG，不超过 100 KB；已有时替换。
func (s *Service) SetManualIcon(ctx context.Context, actor Actor, ref IconRef, data []byte) (*ManualIcon, error) {
	ref, err := normalizeIconRef(ref)
	if err != nil {
		return nil, err
	}
	if len(data) > favicon.MaxIcon {
		return nil, &FieldError{Field: "file", Message: fmt.Sprintf("must not exceed %d KB", favicon.MaxIcon>>10)}
	}
	contentType := favicon.ImageType(data, true)
	if contentType == "" {
		return nil, &FieldError{Field: "file", Message: "must be a PNG, GIF, JPEG, WebP, ICO or SVG image"}
	}
	return s.storeManualIcon(ctx, actor, ref, &favicon.Icon{ContentType: contentType, Data: data}, "上传")
}

// FetchManualIcon 按下载代理设置下载管理员给出的图片地址，作为手动图标保存。
func (s *Service) FetchManualIcon(ctx context.Context, actor Actor, ref IconRef, target string) (*ManualIcon, error) {
	ref, err := normalizeIconRef(ref)
	if err != nil {
		return nil, err
	}
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := s.downloadProxyConfig(ctx, doc, doc.Settings.DownloadProxy)
	if err != nil {
		return nil, &FieldError{Field: "url", Message: "download proxy is unavailable: " + err.Error()}
	}
	fetchCtx, cancel := context.WithTimeout(ctx, faviconTimeout)
	defer cancel()
	var icon *favicon.Icon
	if s.FetchIconURL != nil {
		icon, err = s.FetchIconURL(fetchCtx, cfg, target)
	} else {
		fetcher := favicon.New(cfg)
		defer fetcher.Close()
		icon, err = fetcher.FetchImage(fetchCtx, target)
	}
	switch {
	case errors.Is(err, favicon.ErrForbidden):
		return nil, &FieldError{Field: "url", Message: "must be an http or https URL on a public domain (ports 80 and 443 only): " + err.Error()}
	case errors.Is(err, favicon.ErrNoIcon):
		return nil, &FieldError{Field: "url", Message: "did not return a PNG, GIF, JPEG, WebP, ICO or SVG image of at most 100 KB: " + err.Error()}
	case err != nil:
		return nil, &FieldError{Field: "url", Message: "download failed: " + err.Error()}
	}
	return s.storeManualIcon(ctx, actor, ref, icon, "从网址获取")
}

func (s *Service) storeManualIcon(ctx context.Context, actor Actor, ref IconRef, icon *favicon.Icon, how string) (*ManualIcon, error) {
	now := s.Now()
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		if err := q.PutManualIcon(ctx, dbq.PutManualIconParams{
			Kind: ref.Kind, Name: ref.Name, ContentType: icon.ContentType, Data: icon.Data, UpdatedAt: now.Unix(), UpdatedBy: actor.Name,
		}); err != nil {
			return err
		}
		return audit.Write(ctx, q, now, audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionIconSet, Object: ref.Kind + ":" + ref.Name,
			Summary: fmt.Sprintf("%s设置 %s 的图标（%s，%d 字节）", how, manualIconLabel(ref), icon.ContentType, len(icon.Data)),
		})
	})
	if err != nil {
		return nil, err
	}
	return &ManualIcon{Kind: ref.Kind, Name: ref.Name, ContentType: icon.ContentType, Size: int64(len(icon.Data)), UpdatedAt: time.Unix(now.Unix(), 0), UpdatedBy: actor.Name}, nil
}

// DeleteManualIcons 删除手动图标，返回实际删除的个数。删除后主域名恢复使用自动获取的图标，服务恢复使用代表域名的图标。
func (s *Service) DeleteManualIcons(ctx context.Context, actor Actor, refs []IconRef) (int, error) {
	normalized := make([]IconRef, 0, len(refs))
	for _, ref := range refs {
		item, err := normalizeIconRef(ref)
		if err != nil {
			return 0, err
		}
		normalized = append(normalized, item)
	}
	deleted := 0
	now := s.Now()
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		for _, ref := range normalized {
			count, err := q.DeleteManualIcon(ctx, dbq.DeleteManualIconParams{Kind: ref.Kind, Name: ref.Name})
			if err != nil {
				return err
			}
			if count == 0 {
				continue
			}
			deleted++
			if err := audit.Write(ctx, q, now, audit.Entry{
				Actor: actor.Name, SourceIP: actor.IP, Action: ActionIconDelete, Object: ref.Kind + ":" + ref.Name,
				Summary: "删除 " + manualIconLabel(ref) + " 的手动图标",
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return deleted, err
}
