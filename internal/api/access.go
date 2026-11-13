package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"proxystack-web/internal/app"
)

type accessStatusOutput struct {
	Body app.AccessStatus
}

// 时间范围参数：From、To 是 Unix 秒（To 不含），按设置时区换算成起止日期。
// huma 不读取未导出嵌入结构体的参数，所以各输入结构体分别声明这些字段。
type accessRangeInput struct {
	From  int64 `query:"from" doc:"开始时间（Unix 秒），按设置时区取所在日期"`
	To    int64 `query:"to" doc:"结束时间（Unix 秒），不含，按设置时区取前一秒所在日期"`
	Limit int64 `query:"limit" minimum:"0" maximum:"500" doc:"返回的域名条数，0 表示 500"`
}

func accessQuery(from, to, limit int64) app.AccessQuery {
	return app.AccessQuery{From: time.Unix(from, 0), To: time.Unix(to, 0), Limit: limit}
}

func (in *accessRangeInput) query() app.AccessQuery { return accessQuery(in.From, in.To, in.Limit) }

type accessUserInput struct {
	ID    int64 `path:"id" minimum:"1"`
	From  int64 `query:"from" doc:"开始时间（Unix 秒），按设置时区取所在日期"`
	To    int64 `query:"to" doc:"结束时间（Unix 秒），不含，按设置时区取前一秒所在日期"`
	Limit int64 `query:"limit" minimum:"0" maximum:"500" doc:"返回的域名条数，0 表示 500"`
}

func (in *accessUserInput) query() app.AccessQuery { return accessQuery(in.From, in.To, in.Limit) }

type accessDomainInput struct {
	Domain string `query:"domain" minLength:"1" maxLength:"253" doc:"域名，先归并为主域名"`
	From   int64  `query:"from" doc:"开始时间（Unix 秒），按设置时区取所在日期"`
	To     int64  `query:"to" doc:"结束时间（Unix 秒），不含，按设置时区取前一秒所在日期"`
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

func registerAccess(api huma.API, s *Server) {
	reg := func(op huma.Operation) huma.Operation {
		op.Tags = []string{"traffic"}
		return secured(op)
	}
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
	huma.Register(api, reg(huma.Operation{OperationID: "listAccessDomainUsers", Method: http.MethodGet, Path: "/api/access/domain-users", Summary: "访问过某个主域名的用户"}),
		func(ctx context.Context, in *accessDomainInput) (*accessDomainUsersOutput, error) {
			result, err := s.svc.AccessDomainUsersData(ctx, in.Domain, in.query())
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
