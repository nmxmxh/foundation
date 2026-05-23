package bulk

import (
	"context"
	"errors"
	"maps"
	"sort"
	"sync"
)

var ErrNotFound = errors.New("bulk state not found")

type StateStore interface {
	SavePlan(context.Context, TransferPlan) error
	LoadPlan(context.Context, string, string) (TransferPlan, error)
	SaveReceipt(context.Context, PartReceipt) error
	LoadReceipt(context.Context, string, string, int) (PartReceipt, error)
	ListReceipts(context.Context, string, string) ([]PartReceipt, error)
	SaveManifest(context.Context, TransferManifest) error
	LoadManifest(context.Context, string, string) (TransferManifest, error)
}

type MemoryStateStore struct {
	mu        sync.RWMutex
	plans     map[string]TransferPlan
	receipts  map[string]map[int]PartReceipt
	manifests map[string]TransferManifest
}

func NewMemoryStateStore() *MemoryStateStore {
	return &MemoryStateStore{
		plans:     map[string]TransferPlan{},
		receipts:  map[string]map[int]PartReceipt{},
		manifests: map[string]TransferManifest{},
	}
}

func (s *MemoryStateStore) SavePlan(_ context.Context, plan TransferPlan) error {
	if s == nil {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	s.plans[stateKey(plan.OrganizationID, plan.TransferID)] = clonePlan(plan)
	return nil
}

func (s *MemoryStateStore) LoadPlan(_ context.Context, orgID, transferID string) (TransferPlan, error) {
	if s == nil {
		return TransferPlan{}, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	plan, ok := s.plans[stateKey(orgID, transferID)]
	if !ok {
		return TransferPlan{}, ErrNotFound
	}
	return clonePlan(plan), nil
}

func (s *MemoryStateStore) SaveReceipt(_ context.Context, receipt PartReceipt) error {
	if s == nil {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	key := stateKey(receipt.OrganizationID, receipt.TransferID)
	if s.receipts[key] == nil {
		s.receipts[key] = map[int]PartReceipt{}
	}
	s.receipts[key][receipt.PartNumber] = receipt
	return nil
}

func (s *MemoryStateStore) LoadReceipt(_ context.Context, orgID, transferID string, partNumber int) (PartReceipt, error) {
	if s == nil {
		return PartReceipt{}, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	receipt, ok := s.receipts[stateKey(orgID, transferID)][partNumber]
	if !ok {
		return PartReceipt{}, ErrNotFound
	}
	return receipt, nil
}

func (s *MemoryStateStore) ListReceipts(_ context.Context, orgID, transferID string) ([]PartReceipt, error) {
	if s == nil {
		return nil, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := s.receipts[stateKey(orgID, transferID)]
	if len(items) == 0 {
		return []PartReceipt{}, nil
	}
	out := make([]PartReceipt, 0, len(items))
	for _, receipt := range items {
		out = append(out, receipt)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].PartNumber < out[j].PartNumber
	})
	return out, nil
}

func (s *MemoryStateStore) SaveManifest(_ context.Context, manifest TransferManifest) error {
	if s == nil {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	s.manifests[stateKey(manifest.OrganizationID, manifest.TransferID)] = cloneManifest(manifest)
	return nil
}

func (s *MemoryStateStore) LoadManifest(_ context.Context, orgID, transferID string) (TransferManifest, error) {
	if s == nil {
		return TransferManifest{}, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	manifest, ok := s.manifests[stateKey(orgID, transferID)]
	if !ok {
		return TransferManifest{}, ErrNotFound
	}
	return cloneManifest(manifest), nil
}

func (s *MemoryStateStore) initLocked() {
	if s.plans == nil {
		s.plans = map[string]TransferPlan{}
	}
	if s.receipts == nil {
		s.receipts = map[string]map[int]PartReceipt{}
	}
	if s.manifests == nil {
		s.manifests = map[string]TransferManifest{}
	}
}

func stateKey(orgID, transferID string) string {
	return orgID + "/" + transferID
}

func clonePlan(plan TransferPlan) TransferPlan {
	plan.Attributes = cloneStringMap(plan.Attributes)
	return plan
}

func cloneManifest(manifest TransferManifest) TransferManifest {
	manifest.Parts = append([]PartReceipt(nil), manifest.Parts...)
	return manifest
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

func cloneOptionalStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	return cloneStringMap(in)
}
