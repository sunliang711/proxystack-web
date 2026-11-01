package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"proxystack-web/internal/app"
	"proxystack-web/internal/configdoc"
)

type trafficOverviewInput struct {
	UserID int64 `query:"user_id" minimum:"0" doc:"用户 ID，0 表示全部流量"`
}

type trafficOverviewOutput struct {
	Body app.TrafficOverview
}

type trafficQueryInput struct {
	GroupBy  string `query:"group_by" enum:"subject,stack" default:"subject"`
	Level    string `query:"level" enum:"hour,day,month,year" default:"day"`
	Scope    string `query:"scope" enum:"user,inbound,outbound" default:"user"`
	From     int64  `query:"from" doc:"开始时间（Unix 秒）"`
	To       int64  `query:"to" doc:"结束时间（Unix 秒），不含"`
	Stacks   string `query:"stacks" doc:"stack ID，逗号分隔，空表示全部"`
	Subjects string `query:"subjects" doc:"对象，逗号分隔，空表示全部"`
}

func (in *trafficQueryInput) query() (app.TrafficQuery, error) {
	query := app.TrafficQuery{Level: in.Level, Scope: in.Scope, From: time.Unix(in.From, 0), To: time.Unix(in.To, 0), GroupBy: in.GroupBy}
	if in.Stacks != "" {
		for _, item := range strings.Split(in.Stacks, ",") {
			id, err := strconv.ParseInt(item, 10, 64)
			if err != nil {
				return query, huma.Error422UnprocessableEntity("invalid stack id: " + item)
			}
			query.Stacks = append(query.Stacks, id)
		}
	}
	if in.Subjects != "" {
		query.Subjects = strings.Split(in.Subjects, ",")
	}
	return query, nil
}

type trafficQueryOutput struct {
	Body app.TrafficResult
}

type trafficSubjectsInput struct {
	Scope string `query:"scope" enum:"user,inbound,outbound" default:"user"`
}

type trafficSubjectsOutput struct {
	Body []app.TrafficSubject
}

type usersUsageOutput struct {
	Body []app.UserUsage
}

type userUsageOutput struct {
	Body app.UserUsage
}

type stackUsageInput struct {
	ID     int64 `path:"id" minimum:"1"`
	UserID int64 `query:"user_id" minimum:"0" doc:"用户 ID，0 表示本 Stack 的全部入站流量"`
}

type stackUsageOutput struct {
	Body app.StackUsage
}

type releasePauseInput struct {
	ID     int64  `path:"id"`
	Reason string `path:"reason" enum:"daily,quota,expired"`
}

type notificationsInput struct {
	Limit int64 `query:"limit" minimum:"1" maximum:"1000" default:"200"`
}

type notificationsOutput struct {
	Body []app.Notification
}

type testNotificationInput struct {
	Body configdoc.NotifySettings
}

type testNotificationOutput struct {
	Body struct {
		Error string `json:"error"`
	}
}

func registerTraffic(api huma.API, s *Server) {
	reg := func(op huma.Operation) huma.Operation {
		op.Tags = []string{"traffic"}
		return secured(op)
	}

	huma.Register(api, reg(huma.Operation{OperationID: "getTrafficOverview", Method: http.MethodGet, Path: "/api/traffic/overview", Summary: "流量总览"}),
		func(ctx context.Context, in *trafficOverviewInput) (*trafficOverviewOutput, error) {
			overview, err := s.svc.TrafficOverviewData(ctx, in.UserID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &trafficOverviewOutput{Body: *overview}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "queryTraffic", Method: http.MethodGet, Path: "/api/traffic/query", Summary: "流量明细"}),
		func(ctx context.Context, in *trafficQueryInput) (*trafficQueryOutput, error) {
			query, err := in.query()
			if err != nil {
				return nil, err
			}
			result, err := s.svc.QueryTraffic(ctx, query)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &trafficQueryOutput{Body: *result}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "exportTraffic", Method: http.MethodGet, Path: "/api/traffic/export", Summary: "导出流量明细（CSV）"}),
		func(ctx context.Context, in *trafficQueryInput) (*exportOutput, error) {
			query, err := in.query()
			if err != nil {
				return nil, err
			}
			result, err := s.svc.QueryTraffic(ctx, query)
			if err != nil {
				return nil, toHTTPError(err)
			}
			data, err := s.svc.TrafficCSV(ctx, result)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &exportOutput{
				ContentType:        "text/csv; charset=utf-8",
				ContentDisposition: fmt.Sprintf(`attachment; filename="traffic-%s-%s.csv"`, in.Scope, in.Level),
				Body:               data,
			}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "listTrafficSubjects", Method: http.MethodGet, Path: "/api/traffic/subjects", Summary: "流量明细的筛选对象"}),
		func(ctx context.Context, in *trafficSubjectsInput) (*trafficSubjectsOutput, error) {
			subjects, err := s.svc.TrafficSubjects(ctx, in.Scope)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &trafficSubjectsOutput{Body: subjects}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "listUsersUsage", Method: http.MethodGet, Path: "/api/traffic/users", Summary: "全部用户的用量和自动暂停"}),
		func(ctx context.Context, _ *struct{}) (*usersUsageOutput, error) {
			usage, err := s.svc.UsersUsage(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &usersUsageOutput{Body: usage}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "getStackUsage", Method: http.MethodGet, Path: "/api/stacks/{id}/usage", Summary: "Stack 的用量和日用量趋势，可指定用户"}),
		func(ctx context.Context, in *stackUsageInput) (*stackUsageOutput, error) {
			usage, err := s.svc.StackUsageDetail(ctx, in.ID, in.UserID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &stackUsageOutput{Body: *usage}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "getUserUsage", Method: http.MethodGet, Path: "/api/users/{id}/usage", Summary: "用户的用量、自动暂停和日用量趋势"}),
		func(ctx context.Context, in *idPath) (*userUsageOutput, error) {
			usage, err := s.svc.UserUsageDetail(ctx, in.ID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &userUsageOutput{Body: *usage}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "releaseAutoPause", Method: http.MethodDelete, Path: "/api/users/{id}/auto-pause/{reason}", Summary: "解除自动暂停", DefaultStatus: http.StatusNoContent}),
		func(ctx context.Context, in *releasePauseInput) (*struct{}, error) {
			if err := s.svc.ReleaseAutoPause(ctx, actorFrom(ctx), in.ID, in.Reason); err != nil {
				return nil, toHTTPError(err)
			}
			return nil, nil
		})

	huma.Register(api, reg(huma.Operation{OperationID: "listNotifications", Method: http.MethodGet, Path: "/api/notifications", Summary: "通知记录"}),
		func(ctx context.Context, in *notificationsInput) (*notificationsOutput, error) {
			items, err := s.svc.ListNotifications(ctx, in.Limit)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &notificationsOutput{Body: items}, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "resendNotification", Method: http.MethodPost, Path: "/api/notifications/{id}/resend", Summary: "重新发送通知", DefaultStatus: http.StatusNoContent}),
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			if err := s.svc.ResendNotification(ctx, in.ID); err != nil {
				return nil, toHTTPError(err)
			}
			return nil, nil
		})
	huma.Register(api, reg(huma.Operation{OperationID: "testNotification", Method: http.MethodPost, Path: "/api/notifications/test", Summary: "用表单当前值发送测试邮件"}),
		func(ctx context.Context, in *testNotificationInput) (*testNotificationOutput, error) {
			out := &testNotificationOutput{}
			if err := s.svc.TestNotification(ctx, in.Body); err != nil {
				out.Body.Error = err.Error()
			}
			return out, nil
		})
}
