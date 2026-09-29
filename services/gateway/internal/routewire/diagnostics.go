package routewire

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var ErrInvalidResult = errors.New("invalid optimizer result")
var resultCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

type ResultDiagnostics struct {
	Status            string
	Warnings          []Warning
	Conflicts         []Conflict
	DataMode          string
	DataAsOf          *time.Time
	CatalogRevision   int64
	ComputationTimeMS int64
}

func DecodeDiagnostics(response *pb.OptimizeResponse) (ResultDiagnostics, error) {
	if response == nil || response.Data == nil || response.Data.CatalogRevision <= 0 {
		return ResultDiagnostics{}, ErrInvalidResult
	}
	out := ResultDiagnostics{CatalogRevision: response.Data.CatalogRevision, ComputationTimeMS: int64(response.ComputationTimeMs)}
	switch response.Status {
	case pb.ResultStatus_RESULT_STATUS_READY, pb.ResultStatus_RESULT_STATUS_PARTIAL:
		if len(response.Routes) == 0 || len(response.Routes) > 3 || len(response.Conflicts) != 0 {
			return ResultDiagnostics{}, ErrInvalidResult
		}
		seen := map[pb.Archetype]bool{}
		for _, plan := range response.Routes {
			if plan == nil || plan.CatalogRevision != out.CatalogRevision || (plan.Result != pb.ResultStatus_RESULT_STATUS_READY && plan.Result != pb.ResultStatus_RESULT_STATUS_PARTIAL) || len(plan.Conflicts) != 0 {
				return ResultDiagnostics{}, ErrInvalidResult
			}
			if _, ok := pb.Archetype_name[int32(plan.Archetype)]; !ok || plan.Archetype == pb.Archetype_ARCHETYPE_UNSPECIFIED || seen[plan.Archetype] {
				return ResultDiagnostics{}, ErrInvalidResult
			}
			seen[plan.Archetype] = true
		}
	case pb.ResultStatus_RESULT_STATUS_NO_FEASIBLE_ROUTE, pb.ResultStatus_RESULT_STATUS_CONFLICT:
		if len(response.Routes) != 0 || (len(response.Conflicts) == 0 && len(response.Warnings) == 0) {
			return ResultDiagnostics{}, ErrInvalidResult
		}
		if response.Status == pb.ResultStatus_RESULT_STATUS_CONFLICT && len(response.Conflicts) == 0 {
			return ResultDiagnostics{}, ErrInvalidResult
		}
	default:
		return ResultDiagnostics{}, ErrInvalidResult
	}
	out.Status = strings.TrimPrefix(response.Status.String(), "RESULT_STATUS_")
	switch response.Data.DataMode {
	case pb.DataMode_DATA_MODE_LIVE, pb.DataMode_DATA_MODE_PREPARED, pb.DataMode_DATA_MODE_SYNTHETIC:
		out.DataMode = strings.ToLower(strings.TrimPrefix(response.Data.DataMode.String(), "DATA_MODE_"))
	default:
		return ResultDiagnostics{}, ErrInvalidResult
	}
	var err error
	out.DataAsOf, err = resultInstant(response.Data.DataAsOf, false)
	if err != nil {
		return ResultDiagnostics{}, err
	}
	out.Warnings, err = WarningsFromProto(response.Warnings)
	if err != nil {
		return ResultDiagnostics{}, err
	}
	out.Conflicts, err = ConflictsFromProto(response.Conflicts)
	if err != nil {
		return ResultDiagnostics{}, err
	}
	return out, nil
}

func WarningsFromProto(items []*pb.Warning) ([]Warning, error) {
	out := make([]Warning, 0, len(items))
	for _, item := range items {
		if item == nil || !validExplanation(item.Code, item.Message) {
			return nil, ErrInvalidResult
		}
		warning := Warning{Code: item.Code, Message: item.Message}
		switch item.Scope {
		case pb.TargetScope_TARGET_SCOPE_ROUTE:
			if len(item.VisitId) != 0 || item.LegPosition != nil {
				return nil, ErrInvalidResult
			}
			warning.Scope = "route"
		case pb.TargetScope_TARGET_SCOPE_VISIT:
			if item.LegPosition != nil {
				return nil, ErrInvalidResult
			}
			id, err := resultUUID(item.VisitId)
			if err != nil {
				return nil, err
			}
			warning.Scope, warning.VisitID = "visit", &id
		case pb.TargetScope_TARGET_SCOPE_LEG:
			if len(item.VisitId) != 0 || item.LegPosition == nil || *item.LegPosition == 0 || *item.LegPosition > 2147483647 {
				return nil, ErrInvalidResult
			}
			position := int64(*item.LegPosition)
			warning.Scope, warning.LegPosition = "leg", &position
		default:
			return nil, ErrInvalidResult
		}
		out = append(out, warning)
	}
	return out, nil
}

func ConflictsFromProto(items []*pb.Conflict) ([]Conflict, error) {
	out := make([]Conflict, 0, len(items))
	for _, item := range items {
		if item == nil || !validExplanation(item.Code, item.Message) {
			return nil, ErrInvalidResult
		}
		visits, err := resultUUIDs(item.VisitIds)
		if err != nil {
			return nil, err
		}
		sessions, err := resultUUIDs(item.SessionIds)
		if err != nil {
			return nil, err
		}
		out = append(out, Conflict{Code: item.Code, Message: item.Message, VisitIds: visits, SessionIDs: sessions})
	}
	return out, nil
}

func validExplanation(code, message string) bool {
	return resultCode.MatchString(code) && strings.TrimSpace(message) != "" && utf8.ValidString(message) && utf8.RuneCountInString(message) <= 512
}

func resultUUID(value []byte) (string, error) {
	id, err := uuid.FromBytes(value)
	if err != nil || id == uuid.Nil {
		return "", ErrInvalidResult
	}
	return id.String(), nil
}

func resultUUIDs(values [][]byte) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		id, err := resultUUID(value)
		if err != nil || seen[id] {
			return nil, ErrInvalidResult
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func resultInstant(value *timestamppb.Timestamp, required bool) (*time.Time, error) {
	if value == nil {
		if required {
			return nil, ErrInvalidResult
		}
		return nil, nil
	}
	if value.CheckValid() != nil {
		return nil, ErrInvalidResult
	}
	instant := value.AsTime().UTC()
	if instant.IsZero() {
		return nil, ErrInvalidResult
	}
	return &instant, nil
}
