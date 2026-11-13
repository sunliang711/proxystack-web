package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"proxystack-web/internal/app"
	"proxystack-web/internal/favicon"
)

type accessStatusOutput struct {
	Body app.AccessStatus
}

// 时间范围参数：From、To 是 Unix 秒（To 不含），按设置时区换算成起止日期。
// huma 不读取未导出嵌入结构体的参数，所以各输入结构体分别声明这些字段。
type accessRangeInput struct {
	From  int64  `query:"from" doc:"开始时间（Unix 秒），按设置时区取所在日期"`
	To    int64  `query:"to" doc:"结束时间（Unix 秒），不含，按设置时区取前一秒所在日期"`
	Limit int64  `query:"limit" minimum:"0" maximum:"500" doc:"返回的行数，0 表示 500"`
	Group string `query:"group" enum:"domain,service" default:"domain" doc:"domain 每个主域名一行；service 把同一服务的主域名合并为一行"`
}

func accessQuery(from, to, limit int64) app.AccessQuery {
	return app.AccessQuery{From: time.Unix(from, 0), To: time.Unix(to, 0), Limit: limit}
}

func (in *accessRangeInput) query() app.AccessQuery {
	query := accessQuery(in.From, in.To, in.Limit)
	query.Group = in.Group == "service"
	return query
}

type accessUserInput struct {
	ID    int64  `path:"id" minimum:"1"`
	From  int64  `query:"from" doc:"开始时间（Unix 秒），按设置时区取所在日期"`
	To    int64  `query:"to" doc:"结束时间（Unix 秒），不含，按设置时区取前一秒所在日期"`
	Limit int64  `query:"limit" minimum:"0" maximum:"500" doc:"返回的行数，0 表示 500"`
	Group string `query:"group" enum:"domain,service" default:"domain" doc:"domain 每个主域名一行；service 把同一服务的主域名合并为一行"`
}

func (in *accessUserInput) query() app.AccessQuery {
	query := accessQuery(in.From, in.To, in.Limit)
	query.Group = in.Group == "service"
	return query
}

type accessDomainInput struct {
	Domain  string `query:"domain" maxLength:"253" doc:"域名，先归并为主域名；与 service 二选一"`
	Service string `query:"service" maxLength:"64" doc:"服务名，列出访问过该服务任一主域名的用户；与 domain 二选一"`
	From    int64  `query:"from" doc:"开始时间（Unix 秒），按设置时区取所在日期"`
	To      int64  `query:"to" doc:"结束时间（Unix 秒），不含，按设置时区取前一秒所在日期"`
}

func (in *accessDomainInput) query() app.AccessQuery { return accessQuery(in.From, in.To, 0) }

type accessExportInput struct {
	User int64 `query:"user" minimum:"0" doc:"用户 ID，0 表示全部用户的域名排行"`
	From int64 `query:"from" doc:"开始时间（Unix 秒），按设置时区取所在日期"`
	To   int64 `query:"to" doc:"结束时间（Unix 秒），不含，按设置时区取前一秒所在日期"`
}

type accessDomainsOutput struct {
	Body app.AccessDomains
}

type accessDomainUsersOutput struct {
	Body app.AccessDomainUsers
}

type blockDomainInput struct {
	Body struct {
		Domain  string `json:"domain" minLength:"1" maxLength:"253" doc:"域名，先归并为主域名"`
		UserID  int64  `json:"user_id" minimum:"0" doc:"用户 ID，0 表示全局名单"`
		Blocked bool   `json:"blocked" doc:"true 加入名单，false 移出名单"`
	}
}

type blockDomainOutput struct {
	Body app.BlockDomainResult
}

type faviconInput struct {
	Domain      string `query:"domain" maxLength:"253" doc:"域名，先归并为主域名；与 service 二选一"`
	Service     string `query:"service" maxLength:"64" doc:"服务名，只返回该服务的手动图标；与 domain 二选一"`
	Version     string `query:"v" maxLength:"32" doc:"页面在图标变化后改变它，让浏览器重新请求；服务端忽略"`
	IfNoneMatch string `header:"If-None-Match"`
}

type faviconOutput struct {
	Status       int
	ETag         string `header:"ETag"`
	ContentType  string `header:"Content-Type"`
	CacheControl string `header:"Cache-Control"`
	// 图标来自外部网站，禁止浏览器猜测类型，并且即使被直接打开也不执行任何内容。
	NoSniff string `header:"X-Content-Type-Options"`
	CSP     string `header:"Content-Security-Policy"`
	Body    []byte
}

type manualIconsOutput struct {
	Body []app.ManualIcon
}

type manualIconOutput struct {
	Body app.ManualIcon
}

type manualIconUploadInput struct {
	RawBody huma.MultipartFormFiles[struct {
		File huma.FormFile `form:"file" required:"true" doc:"图片文件；另带表单字段 kind（domain 或 service）和 name（主域名或服务名）"`
	}]
}

type manualIconFetchInput struct {
	Body struct {
		app.IconRef
		URL string `json:"url" minLength:"1" maxLength:"2048" doc:"图片地址，http 或 https"`
	}
}

type manualIconDeleteInput struct {
	Body struct {
		Items []app.IconRef `json:"items" minItems:"1" maxItems:"500"`
	}
}

type manualIconDeleteOutput struct {
	Body struct {
		Deleted int `json:"deleted"`
	}
}

// etagMatches 判断 If-None-Match 是否包含该 ETag（含 * 和弱校验形式）。
func etagMatches(header, etag string) bool {
	for _, item := range strings.Split(header, ",") {
		item = strings.TrimPrefix(strings.TrimSpace(item), "W/")
		if item == "*" || item == etag {
			return true
		}
	}
	return false
}

func registerAccess(api huma.API, s *Server) {
	reg := func(op huma.Operation) huma.Operation {
		op.Tags = []string{"traffic"}
		return secured(op)
	}
	huma.Register(api, reg(huma.Operation{OperationID: "getAccessFavicon", Method: http.MethodGet, Path: "/api/access/favicon", Summary: "主域名的网站图标",
		Description: "按主域名：优先返回手动设置的图标，其次是缓存，缓存没有时经下载代理获取。按服务：只返回服务的手动图标。" +
			"没有图标时返回 404，页面改用其他候选或首字母色块。缓存一天并带 ETag，用 If-None-Match 确认时未变化返回 304。"}),
		func(ctx context.Context, in *faviconInput) (*faviconOutput, error) {
			var (
				icon *favicon.Icon
				err  error
			)
			switch {
			case in.Domain != "" && in.Service != "":
				return nil, huma.Error422UnprocessableEntity("domain and service are mutually exclusive")
			case in.Service != "":
				icon, err = s.svc.AccessServiceIcon(ctx, in.Service)
			case in.Domain != "":
				icon, err = s.svc.AccessFavicon(ctx, in.Domain)
			default:
				return nil, huma.Error422UnprocessableEntity("domain or service is required")
			}
			if err != nil {
				return nil, toHTTPError(err)
			}
			sum := sha256.Sum256(append([]byte(icon.ContentType+"\n"), icon.Data...))
			out := &faviconOutput{
				Status: http.StatusOK, ETag: `"` + hex.EncodeToString(sum[:8]) + `"`, ContentType: icon.ContentType,
				// 浏览器缓存一天；手动图标变化后页面在地址里换一个版本号（由手动图标列表算出），重新请求。
				CacheControl: "private, max-age=86400", NoSniff: "nosniff", CSP: "default-src 'none'; sandbox; frame-ancestors 'none'", Body: icon.Data,
			}
			if etagMatches(in.IfNoneMatch, out.ETag) {
				out.Status, out.ContentType, out.Body = http.StatusNotModified, "", nil
			}
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "listManualIcons", Method: http.MethodGet, Path: "/api/access/icons", Summary: "手动设置的网站图标"}),
		func(ctx context.Context, _ *struct{}) (*manualIconsOutput, error) {
			items, err := s.svc.ListManualIcons(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &manualIconsOutput{Body: items}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "uploadManualIcon", Method: http.MethodPost, Path: "/api/access/icons/upload",
		Summary: "上传手动图标", Description: "接受 PNG、GIF、JPEG、WebP、ICO 和 SVG，不超过 100 KB；已有时替换。",
		MaxBodyBytes: favicon.MaxIcon + 64<<10}),
		func(ctx context.Context, in *manualIconUploadInput) (*manualIconOutput, error) {
			form := in.RawBody.Data()
			data, err := io.ReadAll(io.LimitReader(form.File, favicon.MaxIcon+1))
			if err != nil {
				return nil, toHTTPError(err)
			}
			value := func(key string) string {
				if values := in.RawBody.Form.Value[key]; len(values) > 0 {
					return values[0]
				}
				return ""
			}
			icon, err := s.svc.SetManualIcon(ctx, actorFrom(ctx), app.IconRef{Kind: value("kind"), Name: value("name")}, data)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &manualIconOutput{Body: *icon}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "fetchManualIcon", Method: http.MethodPost, Path: "/api/access/icons/fetch",
		Summary: "从网址获取手动图标", Description: "按下载代理设置下载图片；只访问公网域名的 80、443 端口，格式和大小限制与上传相同。"}),
		func(ctx context.Context, in *manualIconFetchInput) (*manualIconOutput, error) {
			icon, err := s.svc.FetchManualIcon(ctx, actorFrom(ctx), app.IconRef{Kind: in.Body.Kind, Name: in.Body.Name}, in.Body.URL)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &manualIconOutput{Body: *icon}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "deleteManualIcons", Method: http.MethodPost, Path: "/api/access/icons/delete",
		Summary: "删除手动图标", Description: "删除后主域名恢复使用自动获取的图标，服务恢复使用代表域名的图标。"}),
		func(ctx context.Context, in *manualIconDeleteInput) (*manualIconDeleteOutput, error) {
			deleted, err := s.svc.DeleteManualIcons(ctx, actorFrom(ctx), in.Body.Items)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &manualIconDeleteOutput{}
			out.Body.Deleted = deleted
			return out, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "getAccessStatus", Method: http.MethodGet, Path: "/api/access/status", Summary: "访问域名统计的运行状态"}),
		func(ctx context.Context, _ *struct{}) (*accessStatusOutput, error) {
			status, err := s.svc.AccessStatusData(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &accessStatusOutput{Body: *status}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "listAccessDomains", Method: http.MethodGet, Path: "/api/access/domains", Summary: "全部用户的主域名访问排行"}),
		func(ctx context.Context, in *accessRangeInput) (*accessDomainsOutput, error) {
			result, err := s.svc.AccessTopDomains(ctx, in.query())
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &accessDomainsOutput{Body: *result}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "listAccessDomainUsers", Method: http.MethodGet, Path: "/api/access/domain-users", Summary: "访问过某个主域名或服务的用户"}),
		func(ctx context.Context, in *accessDomainInput) (*accessDomainUsersOutput, error) {
			var (
				result *app.AccessDomainUsers
				err    error
			)
			switch {
			case in.Service != "" && in.Domain != "":
				return nil, huma.Error422UnprocessableEntity("domain and service are mutually exclusive")
			case in.Service != "":
				result, err = s.svc.AccessServiceUsers(ctx, in.Service, in.query())
			case in.Domain != "":
				result, err = s.svc.AccessDomainUsersData(ctx, in.Domain, in.query())
			default:
				return nil, huma.Error422UnprocessableEntity("domain or service is required")
			}
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &accessDomainUsersOutput{Body: *result}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "setDomainBlocked", Method: http.MethodPost, Path: "/api/access/block", Summary: "把主域名加入或移出域名黑名单（应用配置后生效）"}),
		func(ctx context.Context, in *blockDomainInput) (*blockDomainOutput, error) {
			result, err := s.svc.SetDomainBlocked(ctx, actorFrom(ctx), in.Body.Domain, in.Body.UserID, in.Body.Blocked)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &blockDomainOutput{Body: *result}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "listUserAccessDomains", Method: http.MethodGet, Path: "/api/users/{id}/access", Summary: "用户的主域名访问排行"}),
		func(ctx context.Context, in *accessUserInput) (*accessDomainsOutput, error) {
			result, err := s.svc.AccessUserDomains(ctx, in.ID, in.query())
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &accessDomainsOutput{Body: *result}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "exportAccessDomains", Method: http.MethodGet, Path: "/api/access/export", Summary: "导出主域名访问排行（CSV）"}),
		func(ctx context.Context, in *accessExportInput) (*exportOutput, error) {
			var (
				result *app.AccessDomains
				err    error
			)
			name := "all"
			query := accessQuery(in.From, in.To, -1)
			if in.User > 0 {
				result, err = s.svc.AccessUserDomains(ctx, in.User, query)
				name = fmt.Sprintf("user-%d", in.User)
			} else {
				result, err = s.svc.AccessTopDomains(ctx, query)
			}
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &exportOutput{
				ContentType:        "text/csv; charset=utf-8",
				ContentDisposition: fmt.Sprintf(`attachment; filename="access-%s-%s-%s.csv"`, name, result.FromDay, result.ToDay),
				Body:               app.AccessDomainsCSV(result, in.User > 0),
			}, nil
		})
}
