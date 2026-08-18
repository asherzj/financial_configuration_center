package domain

import (
	"errors"
	"fmt"
	"sort"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
)

var ErrInvariant = errors.New("overlay invariant violated")

// Scope identifies the exact configuration visibility range. An empty Stage
// on a Rule is an environment-wide layer, not a wildcard submitted by callers.
type Scope struct {
	Region      string `json:"region"`
	Environment string `json:"environment"`
	Stage       string `json:"stage"`
}

// Query identifies one collection and one full scope to evaluate.
type Query struct {
	Collection    string
	Scope         Scope
	PreviewBucket *int32
}

type Action string

const (
	ActionAdd    Action = "ADD"
	ActionModify Action = "MODIFY"
	ActionDelete Action = "DELETE"
)

type BucketRange struct {
	Start int32 `json:"start"`
	End   int32 `json:"end"`
}

// Rule is the distribution-visible OverlayRule representation. A rule is
// effective only after activation and before expiration.
type Rule struct {
	ID                string                  `json:"id"`
	Collection        string                  `json:"collection"`
	Scope             Scope                   `json:"scope"`
	RecordKey         string                  `json:"recordKey"`
	Action            Action                  `json:"action"`
	Content           map[string]string       `json:"content,omitempty"`
	RolloutRanges     []BucketRange           `json:"rolloutRanges"`
	ConfigRevision    catalog.ConfigRevision  `json:"configRevision"`
	ReleaseOrderID    string                  `json:"releaseOrderId"`
	EffectiveFrom     *time.Time              `json:"effectiveFrom,omitempty"`
	EffectiveUntil    *time.Time              `json:"effectiveUntil,omitempty"`
	ActivatedRevision *catalog.ConfigRevision `json:"activatedRevision,omitempty"`
	ActivatedAt       *time.Time              `json:"activatedAt,omitempty"`
	ExpiredRevision   *catalog.ConfigRevision `json:"expiredRevision,omitempty"`
	ExpiredAt         *time.Time              `json:"expiredAt,omitempty"`
	CreatedAt         time.Time               `json:"createdAt"`
	CreatedBy         string                  `json:"createdBy"`
	UpdatedAt         time.Time               `json:"updatedAt"`
	UpdatedBy         string                  `json:"updatedBy"`
}

// Evaluate returns immutable effective records in RecordKey order.
func Evaluate(query Query, base []catalog.ConfigurationRecord, rules []Rule) ([]catalog.ConfigurationRecord, error) {
	records := make(map[string]catalog.ConfigurationRecord, len(base))
	for _, record := range base {
		if record.Collection != query.Collection || record.Environment != query.Scope.Environment {
			continue
		}
		record.Data = cloneData(record.Data)
		records[record.RecordKey] = record
	}

	for _, stage := range []string{"", query.Scope.Stage} {
		for _, rule := range rules {
			if rule.Collection != query.Collection ||
				rule.Scope.Region != query.Scope.Region ||
				rule.Scope.Environment != query.Scope.Environment ||
				rule.Scope.Stage != stage ||
				rule.ActivatedRevision == nil || rule.ExpiredRevision != nil {
				continue
			}
			if len(rule.RolloutRanges) > 0 {
				if query.PreviewBucket == nil {
					continue
				}
				ranges, err := canonicalRanges(rule.RolloutRanges)
				if err != nil {
					return nil, fmt.Errorf("%w: rule %q: %v", ErrInvariant, rule.ID, err)
				}
				matched := false
				for _, bucketRange := range ranges {
					if *query.PreviewBucket >= bucketRange[0] && *query.PreviewBucket <= bucketRange[1] {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}
			if (rule.Action == ActionDelete && rule.Content != nil) ||
				((rule.Action == ActionAdd || rule.Action == ActionModify) && rule.Content == nil) {
				return nil, fmt.Errorf("%w: %s rule %q has invalid content", ErrInvariant, rule.Action, rule.ID)
			}
			record, exists := records[rule.RecordKey]
			switch rule.Action {
			case ActionAdd:
				if exists {
					return nil, fmt.Errorf("%w: ADD rule %q targets existing record %q", ErrInvariant, rule.ID, rule.RecordKey)
				}
				record = catalog.ConfigurationRecord{
					Collection:  query.Collection,
					Environment: query.Scope.Environment,
					RecordKey:   rule.RecordKey,
				}
			case ActionModify:
				if !exists {
					return nil, fmt.Errorf("%w: MODIFY rule %q targets missing record %q", ErrInvariant, rule.ID, rule.RecordKey)
				}
			case ActionDelete:
				if !exists {
					return nil, fmt.Errorf("%w: DELETE rule %q targets missing record %q", ErrInvariant, rule.ID, rule.RecordKey)
				}
				delete(records, rule.RecordKey)
				continue
			default:
				return nil, fmt.Errorf("%w: rule %q has unsupported action %q", ErrInvariant, rule.ID, rule.Action)
			}
			record.Data = cloneData(rule.Content)
			record.ConfigRevision = rule.ConfigRevision
			records[rule.RecordKey] = record
		}
		if query.Scope.Stage == "" {
			break
		}
	}

	keys := make([]string, 0, len(records))
	for key := range records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	effective := make([]catalog.ConfigurationRecord, len(keys))
	for index, key := range keys {
		effective[index] = records[key]
	}
	return effective, nil
}

func cloneData(data map[string]string) map[string]string {
	cloned := make(map[string]string, len(data))
	for key, value := range data {
		cloned[key] = value
	}
	return cloned
}
