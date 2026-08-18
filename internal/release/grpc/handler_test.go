package grpc_test

import (
	"context"
	"testing"

	"github.com/asherzj/financial_configuration_center/internal/release/application"
	release "github.com/asherzj/financial_configuration_center/internal/release/domain"
	releasegrpc "github.com/asherzj/financial_configuration_center/internal/release/grpc"
	commonv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/common/v1"
	controlv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/control/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestReleaseHandlerMapsCreateAndAction(t *testing.T) {
	t.Parallel()

	commands := &commandStub{create: application.OrderView{ID: "order", Status: release.OrderInProgress, CurrentStepCode: "base-apply", CurrentStep: release.StepBaseApply, Revision: 1}, act: application.OrderView{ID: "order", Status: release.OrderInProgress, CurrentStepCode: "base-apply", CurrentStep: release.StepBaseApply, Revision: 2}}
	handler, err := releasegrpc.New(commands, actorResolver{})
	if err != nil {
		t.Fatal(err)
	}
	created, err := handler.CreateReleaseOrder(context.Background(), &controlv1.CreateReleaseOrderRequest{
		IdempotencyKey: "create-id", ModelCode: "model", ReleaseTypeCode: "direct", Description: "Add route",
		Scope: &commonv1.Scope{Region: "cn", Environment: "production"},
		Items: []*controlv1.ReleaseItemInput{{Action: commonv1.ChangeAction_CHANGE_ACTION_ADD, After: map[string]string{"code": "visa"}, ExpectedCollectionRevision: 7, PreserveSensitiveFields: []string{"secret"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Detail.Order.Id != "order" || commands.lastCreate.Actor != "operator@example.com" || commands.lastCreate.ActorName != "Operator" || commands.lastCreate.Items[0].ExpectedCollectionRevision != 7 || len(commands.lastCreate.Items[0].PreserveSensitiveFields) != 1 {
		t.Fatalf("create mapping response=%+v command=%+v", created, commands.lastCreate)
	}
	acted, err := handler.ActOnReleaseOrder(context.Background(), &controlv1.ActOnReleaseOrderRequest{
		OrderId: "order", ActionRequestId: "action-id", ExpectedOrderRevision: 1,
		ExpectedCurrentStep: "base-apply", Action: commonv1.ReleaseAction_RELEASE_ACTION_EXECUTE,
	})
	if err != nil {
		t.Fatal(err)
	}
	if acted.Detail.Order.EntityRevision != 2 || commands.lastAct.ExpectedCurrentStep != "base-apply" || commands.lastAct.Action != application.ActionExecute {
		t.Fatalf("action mapping response=%+v command=%+v", acted, commands.lastAct)
	}
	_, err = handler.ActOnReleaseOrder(context.Background(), &controlv1.ActOnReleaseOrderRequest{
		OrderId: "order", ActionRequestId: "approval-id", ExpectedOrderRevision: 2,
		ExpectedCurrentStep: "review", Action: commonv1.ReleaseAction_RELEASE_ACTION_APPROVE, Comment: "reviewed",
	})
	if err != nil || commands.lastAct.Action != application.ActionApprove || commands.lastAct.Comment != "reviewed" || len(commands.lastAct.Roles) != 1 || commands.lastAct.Roles[0] != "RELEASE_APPROVER" {
		t.Fatalf("approval command=%+v err=%v", commands.lastAct, err)
	}
	commands.act = application.OrderView{ID: "order", Status: release.OrderRolledBack, CurrentStepCode: "apply-overlay", CurrentStep: release.StepOverlayApply, CurrentStepStatus: release.StepRolledBack, Revision: 3}
	rolledBack, err := handler.ActOnReleaseOrder(context.Background(), &controlv1.ActOnReleaseOrderRequest{
		OrderId: "order", ActionRequestId: "rollback-id", ExpectedOrderRevision: 2,
		ExpectedCurrentStep: "apply-overlay", Action: commonv1.ReleaseAction_RELEASE_ACTION_ROLLBACK,
	})
	if err != nil || commands.lastAct.Action != application.ActionRollback || rolledBack.Detail.Order.Status != commonv1.ReleaseStatus_RELEASE_STATUS_ROLLED_BACK {
		t.Fatalf("rollback response=%+v command=%+v err=%v", rolledBack, commands.lastAct, err)
	}
}

func TestReleaseHandlerMapsIdempotencyConflict(t *testing.T) {
	t.Parallel()
	handler, err := releasegrpc.New(&commandStub{actErr: release.ErrIdempotencyKeyReused}, actorResolver{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler.ActOnReleaseOrder(context.Background(), &controlv1.ActOnReleaseOrderRequest{
		OrderId: "order", ActionRequestId: "action-id", ExpectedOrderRevision: 1,
		ExpectedCurrentStep: "base-apply", Action: commonv1.ReleaseAction_RELEASE_ACTION_EXECUTE,
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("status = %v, want AlreadyExists", err)
	}
}

func TestReleaseHandlerMapsCompareMismatch(t *testing.T) {
	t.Parallel()
	handler, err := releasegrpc.New(&commandStub{actErr: release.ErrFailedPrecondition}, actorResolver{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler.ActOnReleaseOrder(context.Background(), &controlv1.ActOnReleaseOrderRequest{
		OrderId: "order", ActionRequestId: "compare-id", ExpectedOrderRevision: 3,
		ExpectedCurrentStep: "compare", Action: commonv1.ReleaseAction_RELEASE_ACTION_EXECUTE,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("status = %v, want FailedPrecondition", err)
	}
}

type actorResolver struct{}

func (actorResolver) Subject(context.Context) (string, error)     { return "operator@example.com", nil }
func (actorResolver) DisplayName(context.Context) (string, error) { return "Operator", nil }
func (actorResolver) Roles(context.Context) ([]string, error) {
	return []string{"RELEASE_APPROVER"}, nil
}

type commandStub struct {
	create     application.OrderView
	act        application.OrderView
	lastCreate application.CreateReleaseCommand
	lastAct    application.ActCommand
	actErr     error
}

func (stub *commandStub) CreateRelease(_ context.Context, command application.CreateReleaseCommand) (application.OrderView, error) {
	stub.lastCreate = command
	return stub.create, nil
}

func (stub *commandStub) Act(_ context.Context, command application.ActCommand) (application.OrderView, error) {
	stub.lastAct = command
	return stub.act, stub.actErr
}
