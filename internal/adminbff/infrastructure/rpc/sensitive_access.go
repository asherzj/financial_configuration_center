package rpc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	commonv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/common/v1"
	controlv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/control/v1"
	"github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/control/v1/sensitiveaccessservice"
	bffapp "github.com/asherzj/financial_configuration_center/internal/adminbff/application"
	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	kitexcodes "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	kitexmetadata "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/metadata"
	kitexstatus "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/status"
)

var ErrInvalidSensitiveAccessResponse = errors.New("invalid sensitive access response")

const maxSensitiveResponseLifetime = 65 * time.Second

// SensitiveCredentialAttacher authenticates the end-user Principal on an
// internal RPC context, normally by signing a short-lived Ed25519 JWT. The
// Principal must not be copied into RevealFieldRequest.
type SensitiveCredentialAttacher interface {
	AttachSensitiveCredentials(context.Context, bffapp.SensitivePrincipal) (context.Context, error)
}

type SensitiveAccessClient struct {
	client      sensitiveaccessservice.Client
	credentials SensitiveCredentialAttacher
	clock       func() time.Time
}

func NewSensitiveAccessClient(client sensitiveaccessservice.Client, credentials SensitiveCredentialAttacher) (*SensitiveAccessClient, error) {
	if client == nil || isNil(client) || credentials == nil || isNil(credentials) {
		return nil, errors.New("new BFF sensitive access client: client and credential attacher are required")
	}
	return &SensitiveAccessClient{client: client, credentials: credentials, clock: time.Now}, nil
}

func (client *SensitiveAccessClient) RevealField(ctx context.Context, command bffapp.RevealSensitiveCommand) (bffapp.RevealSensitiveResult, error) {
	if ctx == nil || command.ExpectedRecordRevision == 0 || command.ExpectedRecordRevision > math.MaxInt64 ||
		command.ExpectedCollectionRevision == 0 || command.ExpectedCollectionRevision > math.MaxInt64 ||
		command.ExpectedModelRevision == 0 || command.ExpectedModelRevision > math.MaxInt64 {
		return bffapp.RevealSensitiveResult{}, bffapp.ErrSensitiveInvalid
	}
	authenticated, err := client.credentials.AttachSensitiveCredentials(ctx, cloneSensitivePrincipal(command.Principal))
	if err != nil {
		return bffapp.RevealSensitiveResult{}, fmt.Errorf("authenticate sensitive access RPC: %w", err)
	}
	if authenticated == nil {
		return bffapp.RevealSensitiveResult{}, fmt.Errorf("authenticate sensitive access RPC: %w", ErrInvalidSensitiveAccessResponse)
	}
	metadata := make([]string, 0, 4)
	if command.RequestID != "" {
		metadata = append(metadata, "x-request-id", command.RequestID)
	}
	if command.TraceParent != "" {
		metadata = append(metadata, "traceparent", command.TraceParent)
	}
	if command.TraceState != "" {
		metadata = append(metadata, "tracestate", command.TraceState)
	}
	if len(metadata) != 0 {
		authenticated = kitexmetadata.AppendToOutgoingContext(authenticated, metadata...)
	}
	response, err := client.client.RevealField(authenticated, &controlv1.RevealFieldRequest{
		ModelCode: command.ModelCode,
		Scope: &commonv1.Scope{
			Region: command.Scope.Region, Environment: command.Scope.Environment, Stage: command.Scope.Stage,
		},
		RecordKey: command.RecordKey, FieldName: command.FieldName,
		ExpectedRecordRevision: int64(command.ExpectedRecordRevision), ExpectedCollectionRevision: int64(command.ExpectedCollectionRevision),
		ExpectedModelRevision: int64(command.ExpectedModelRevision), ExpectedServerEpoch: command.ExpectedServerEpoch,
		ExpectedSnapshotInstance: command.ExpectedSnapshotInstance, ExpectedSnapshotGeneration: command.ExpectedSnapshotGeneration,
		Reason: command.Reason, PreviewBucket: cloneInt32(command.PreviewBucket),
	})
	if err != nil {
		return bffapp.RevealSensitiveResult{}, mapSensitiveAccessError(err)
	}
	if response == nil || response.ExpiresAt == nil || response.ExpiresAt.CheckValid() != nil {
		return bffapp.RevealSensitiveResult{}, ErrInvalidSensitiveAccessResponse
	}
	expiresAt := response.ExpiresAt.AsTime()
	now := client.clock().UTC()
	if !expiresAt.After(now) || expiresAt.After(now.Add(maxSensitiveResponseLifetime)) {
		return bffapp.RevealSensitiveResult{}, ErrInvalidSensitiveAccessResponse
	}
	return bffapp.RevealSensitiveResult{Value: response.Value, ExpiresAt: expiresAt}, nil
}

func mapSensitiveAccessError(err error) error {
	switch kitexstatus.Code(err) {
	case kitexcodes.InvalidArgument:
		return bffapp.ErrSensitiveInvalid
	case kitexcodes.PermissionDenied:
		return bffapp.ErrSensitiveForbidden
	case kitexcodes.Aborted:
		return bffapp.ErrSensitiveAborted
	case kitexcodes.NotFound:
		return bffapp.ErrSensitiveNotFound
	case kitexcodes.FailedPrecondition:
		return bffapp.ErrSensitiveFailedPrecondition
	default:
		return fmt.Errorf("reveal sensitive field through Control Plane: %w", err)
	}
}

func cloneSensitivePrincipal(source bffapp.SensitivePrincipal) bffapp.SensitivePrincipal {
	return bffapp.SensitivePrincipal{
		Subject: source.Subject, DisplayName: source.DisplayName,
		Roles: append([]string(nil), source.Roles...), AllowedScopes: append([]platformauth.ScopePattern(nil), source.AllowedScopes...),
	}
}

var _ bffapp.SensitiveAccessPort = (*SensitiveAccessClient)(nil)
