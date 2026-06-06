package hermes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

const defaultDriftSampleSize = 64

type DriftOptions struct {
	MaxRecords int
	SampleSize int
}

type DriftReport struct {
	Projection     string
	Domain         string
	Collection     string
	OrganizationID string
	Filters        []database.RecordFilter
	SourceCount    int64
	HermesCount    int64
	SourceRoot     string
	HermesRoot     string
	Complete       bool
	Truncated      bool
	Samples        []DriftSample
	Mismatches     []DriftMismatch
}

type DriftSample struct {
	RecordID      string
	SourceHash    string
	HermesHash    string
	Match         bool
	SourceWitness MerkleWitness
	HermesWitness MerkleWitness
}

type DriftMismatch struct {
	RecordID   string
	Reason     string
	SourceHash string
	HermesHash string
}

type MerkleWitness struct {
	RecordID string
	LeafHash string
	Siblings []MerkleSibling
}

type MerkleSibling struct {
	Side string
	Hash string
}

func (r DriftReport) OK() bool {
	return r.Complete &&
		!r.Truncated &&
		r.SourceCount == r.HermesCount &&
		r.SourceRoot == r.HermesRoot &&
		len(r.Mismatches) == 0
}

func (s *Store) CheckDrift(ctx context.Context, projection string, source database.StateStore, query Query, opts DriftOptions) (DriftReport, error) {
	if source == nil {
		return DriftReport{}, ErrInvalidEvent
	}
	if err := ctxErr(ctx); err != nil {
		return DriftReport{}, err
	}
	part, err := s.partition(projection)
	if err != nil {
		return DriftReport{}, err
	}
	query = normalizeQuery(query)
	if query.OrganizationID == "" {
		return DriftReport{}, ErrInvalidEvent
	}
	recordQuery := query.RecordQuery()
	recordQuery.Limit = opts.MaxRecords
	opts = normalizeDriftOptions(opts, part.spec)
	report := DriftReport{
		Projection:     part.spec.Name,
		Domain:         part.spec.Domain,
		Collection:     part.spec.Collection,
		OrganizationID: query.OrganizationID,
		Filters:        append([]database.RecordFilter(nil), recordQuery.Filters...),
		Complete:       true,
	}
	sourceCount, err := source.CountRecords(ctx, part.spec.Domain, part.spec.Collection, query.OrganizationID, recordQuery)
	if err != nil {
		return DriftReport{}, err
	}
	hermesCount, err := s.Count(ctx, projection, query, Fence{})
	if err != nil {
		return DriftReport{}, err
	}
	report.SourceCount = sourceCount
	report.HermesCount = hermesCount
	if sourceCount != hermesCount {
		report.Mismatches = append(report.Mismatches, DriftMismatch{Reason: "count_mismatch"})
	}
	report.Complete = sourceCount <= int64(opts.MaxRecords) && hermesCount <= int64(opts.MaxRecords)
	report.Truncated = !report.Complete
	if report.Truncated {
		report.Mismatches = append(report.Mismatches, DriftMismatch{Reason: "bounded_sample_truncated"})
	}
	sourceRecords, err := source.ListRecords(ctx, part.spec.Domain, part.spec.Collection, query.OrganizationID, recordQuery)
	if err != nil {
		return DriftReport{}, err
	}
	hermesSet, err := s.driftRecordSet(ctx, projection, Query{
		OrganizationID: query.OrganizationID,
		Limit:          opts.MaxRecords,
		Plan:           query.Plan,
	})
	if err != nil {
		return DriftReport{}, err
	}
	sourceSet, err := newDriftRecordSet(sourceRecords)
	if err != nil {
		return DriftReport{}, err
	}
	report.SourceRoot = sourceSet.root
	report.HermesRoot = hermesSet.root
	report.Samples = buildDriftSamples(sourceSet, hermesSet, opts.SampleSize)
	report.Mismatches = append(report.Mismatches, compareDriftSets(sourceSet, hermesSet)...)
	return report, nil
}

func normalizeDriftOptions(opts DriftOptions, spec ProjectionSpec) DriftOptions {
	if opts.MaxRecords <= 0 || opts.MaxRecords > spec.MaxRecords {
		opts.MaxRecords = spec.MaxRecords
	}
	if opts.SampleSize <= 0 {
		opts.SampleSize = defaultDriftSampleSize
	}
	if opts.SampleSize > opts.MaxRecords {
		opts.SampleSize = opts.MaxRecords
	}
	return opts
}

type driftRecordSet struct {
	ids    []string
	leaves []driftLeaf
	hashes map[string][32]byte
	index  map[string]int
	tree   merkleTree
	root   string
}

type driftLeaf struct {
	recordID string
	hash     [32]byte
}

func newDriftRecordSet(records []database.DomainRecord) (driftRecordSet, error) {
	builder := newDriftRecordSetBuilder(len(records))
	for _, rec := range records {
		if err := builder.addRecord(rec); err != nil {
			return driftRecordSet{}, err
		}
	}
	return builder.build(), nil
}

func (s *Store) driftRecordSet(ctx context.Context, projection string, query Query) (driftRecordSet, error) {
	part, err := s.partition(projection)
	if err != nil {
		return driftRecordSet{}, err
	}
	builder := newDriftRecordSetBuilder(query.Limit)
	_, err = part.forEachView(ctx, query, Fence{}, func(view RecordView) error {
		return builder.addView(view)
	})
	if err != nil {
		return driftRecordSet{}, err
	}
	return builder.build(), nil
}

type driftRecordSetBuilder struct {
	leaves []driftLeaf
	hashes map[string][32]byte
	buffer []byte
	keys   []string
}

func newDriftRecordSetBuilder(size int) *driftRecordSetBuilder {
	if size < 0 {
		size = 0
	}
	return &driftRecordSetBuilder{
		leaves: make([]driftLeaf, 0, size),
		hashes: make(map[string][32]byte, size),
		buffer: make([]byte, 0, 512),
		keys:   make([]string, 0, 8),
	}
}

func (b *driftRecordSetBuilder) addRecord(rec database.DomainRecord) error {
	sum := b.hashRecordParts(rec.Domain, rec.Collection, rec.OrganizationID, rec.RecordID, rec.Data, rec.Vector)
	b.addLeaf(rec.RecordID, sum)
	return nil
}

func (b *driftRecordSetBuilder) addView(view RecordView) error {
	sum := b.hashRecordParts(view.Domain, view.Collection, view.OrganizationID, view.RecordID, view.Data, view.Vector)
	b.addLeaf(view.RecordID, sum)
	return nil
}

func (b *driftRecordSetBuilder) addLeaf(recordID string, sum [32]byte) {
	b.leaves = append(b.leaves, driftLeaf{recordID: recordID, hash: sum})
	b.hashes[recordID] = sum
}

func (b *driftRecordSetBuilder) hashRecordParts(domain, collection, organizationID, recordID string, data database.RecordData, vector []float32) [32]byte {
	b.buffer = b.buffer[:0]
	b.keys = b.keys[:0]
	b.buffer = appendHashPart(b.buffer, "hermes-drift-v1")
	b.buffer = appendHashPart(b.buffer, domain)
	b.buffer = appendHashPart(b.buffer, collection)
	b.buffer = appendHashPart(b.buffer, organizationID)
	b.buffer = appendHashPart(b.buffer, recordID)
	b.buffer = appendCanonicalRecordData(b.buffer, data)
	b.buffer = appendCanonicalVector(b.buffer, vector)
	return sha256.Sum256(b.buffer)
}

func (b *driftRecordSetBuilder) build() driftRecordSet {
	leaves := b.leaves
	hashes := b.hashes
	sort.Slice(leaves, func(i int, j int) bool {
		return leaves[i].recordID < leaves[j].recordID
	})
	ids := make([]string, 0, len(leaves))
	index := make(map[string]int, len(leaves))
	for i, leaf := range leaves {
		ids = append(ids, leaf.recordID)
		index[leaf.recordID] = i
	}
	tree := newMerkleTree(leaves)
	return driftRecordSet{
		ids:    ids,
		leaves: leaves,
		hashes: hashes,
		index:  index,
		tree:   tree,
		root:   tree.root(),
	}
}

func appendHashPart(out []byte, value string) []byte {
	out = strconv.AppendInt(out, int64(len(value)), 10)
	out = append(out, ':')
	out = append(out, value...)
	return append(out, 0)
}

func appendCanonicalVector(out []byte, values []float32) []byte {
	out = appendHashPart(out, "vector")
	out = strconv.AppendInt(out, int64(len(values)), 10)
	out = append(out, 0)
	for _, value := range values {
		out = strconv.AppendFloat(out, float64(value), 'g', -1, 32)
		out = append(out, 0)
	}
	return out
}

func appendCanonicalRecordData(out []byte, values database.RecordData) []byte {
	out = appendHashPart(out, "object")
	values = values.Normalize()
	out = strconv.AppendInt(out, int64(len(values)), 10)
	out = append(out, 0)
	for _, field := range values {
		out = appendHashPart(out, field.Name)
		out = appendCanonicalRecordValue(out, field.Value)
	}
	return out
}

func appendCanonicalRecordValue(out []byte, value database.RecordValue) []byte {
	switch value.Kind {
	case database.RecordValueNull:
		return appendHashPart(out, "null")
	case database.RecordValueString:
		out = appendHashPart(out, "string")
		return appendHashPart(out, value.Text)
	case database.RecordValueBool:
		out = appendHashPart(out, "bool")
		out = strconv.AppendBool(out, value.Text == "true" || value.Text == "1")
		return append(out, 0)
	case database.RecordValueInt:
		parsed, err := strconv.ParseInt(value.Text, 10, 64)
		if err != nil {
			return appendHashPart(out, value.Text)
		}
		return appendCanonicalInt(out, parsed)
	case database.RecordValueUint:
		parsed, err := strconv.ParseUint(value.Text, 10, 64)
		if err != nil {
			return appendHashPart(out, value.Text)
		}
		return appendCanonicalUint(out, parsed)
	case database.RecordValueFloat:
		parsed, err := strconv.ParseFloat(value.Text, 64)
		if err != nil {
			return appendHashPart(out, value.Text)
		}
		return appendCanonicalFloat(out, parsed, 64)
	case database.RecordValueRaw:
		return appendHashPart(out, canonicalRawJSONForDrift(value.Raw))
	default:
		return appendHashPart(out, value.Text)
	}
}

func canonicalRawJSONForDrift(raw []byte) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || !json.Valid(trimmed) {
		return string(trimmed)
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return string(trimmed)
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return string(trimmed)
	}
	return string(canonical)
}

func appendCanonicalInt(out []byte, value int64) []byte {
	out = appendHashPart(out, "number")
	out = strconv.AppendInt(out, value, 10)
	return append(out, 0)
}

func appendCanonicalUint(out []byte, value uint64) []byte {
	out = appendHashPart(out, "number")
	out = strconv.AppendUint(out, value, 10)
	return append(out, 0)
}

func appendCanonicalFloat(out []byte, value float64, bitSize int) []byte {
	out = appendHashPart(out, "number")
	out = strconv.AppendFloat(out, value, 'g', -1, bitSize)
	return append(out, 0)
}

type merkleTree struct {
	levels [][][32]byte
}

func newMerkleTree(leaves []driftLeaf) merkleTree {
	if len(leaves) == 0 {
		return merkleTree{}
	}
	level := make([][32]byte, len(leaves))
	for i, leaf := range leaves {
		level[i] = leaf.hash
	}
	levels := [][][32]byte{level}
	for len(level) > 1 {
		level = merkleNextLevel(level)
		levels = append(levels, level)
	}
	return merkleTree{levels: levels}
}

func (t merkleTree) root() string {
	if len(t.levels) == 0 {
		sum := sha256.Sum256([]byte("hermes-drift-empty"))
		return hex.EncodeToString(sum[:])
	}
	rootLevel := t.levels[len(t.levels)-1]
	return hex.EncodeToString(rootLevel[0][:])
}

func (t merkleTree) proof(index int) []MerkleSibling {
	siblings := make([]MerkleSibling, 0)
	for _, level := range t.levels {
		if len(level) <= 1 {
			break
		}
		siblingIndex := index ^ 1
		if siblingIndex >= len(level) {
			siblingIndex = index
		}
		side := "right"
		if siblingIndex < index {
			side = "left"
		}
		sibling := level[siblingIndex]
		siblings = append(siblings, MerkleSibling{Side: side, Hash: hex.EncodeToString(sibling[:])})
		index /= 2
	}
	return siblings
}

func merkleNextLevel(level [][32]byte) [][32]byte {
	next := make([][32]byte, 0, (len(level)+1)/2)
	for i := 0; i < len(level); i += 2 {
		right := level[i]
		if i+1 < len(level) {
			right = level[i+1]
		}
		next = append(next, merkleParent(level[i], right))
	}
	return next
}

func merkleParent(left [32]byte, right [32]byte) [32]byte {
	var payload [68]byte
	copy(payload[:4], "node")
	copy(payload[4:36], left[:])
	copy(payload[36:], right[:])
	return sha256.Sum256(payload[:])
}

func buildDriftSamples(source driftRecordSet, hermes driftRecordSet, sampleSize int) []DriftSample {
	ids := selectDriftSampleIDs(unionSortedIDs(source.ids, hermes.ids), sampleSize)
	samples := make([]DriftSample, 0, len(ids))
	for _, id := range ids {
		sourceHash, sourceOK := source.hashes[id]
		hermesHash, hermesOK := hermes.hashes[id]
		samples = append(samples, DriftSample{
			RecordID:      id,
			SourceHash:    hexDriftHash(sourceHash, sourceOK),
			HermesHash:    hexDriftHash(hermesHash, hermesOK),
			Match:         sourceOK && hermesOK && sourceHash == hermesHash,
			SourceWitness: source.witness(id),
			HermesWitness: hermes.witness(id),
		})
	}
	return samples
}

func (s driftRecordSet) witness(recordID string) MerkleWitness {
	index, ok := s.index[recordID]
	if !ok {
		return MerkleWitness{}
	}
	leaf := s.leaves[index]
	return MerkleWitness{
		RecordID: recordID,
		LeafHash: hex.EncodeToString(leaf.hash[:]),
		Siblings: s.tree.proof(index),
	}
}

func compareDriftSets(source driftRecordSet, hermes driftRecordSet) []DriftMismatch {
	ids := unionSortedIDs(source.ids, hermes.ids)
	mismatches := make([]DriftMismatch, 0)
	for _, id := range ids {
		sourceHash, sourceOK := source.hashes[id]
		hermesHash, hermesOK := hermes.hashes[id]
		switch {
		case !sourceOK:
			mismatches = append(mismatches, DriftMismatch{RecordID: id, Reason: "missing_source", HermesHash: hexDriftHash(hermesHash, true)})
		case !hermesOK:
			mismatches = append(mismatches, DriftMismatch{RecordID: id, Reason: "missing_hermes", SourceHash: hexDriftHash(sourceHash, true)})
		case sourceHash != hermesHash:
			mismatches = append(mismatches, DriftMismatch{
				RecordID:   id,
				Reason:     "hash_mismatch",
				SourceHash: hexDriftHash(sourceHash, true),
				HermesHash: hexDriftHash(hermesHash, true),
			})
		}
	}
	return mismatches
}

func hexDriftHash(sum [32]byte, ok bool) string {
	if !ok {
		return ""
	}
	return hex.EncodeToString(sum[:])
}

func unionSortedIDs(left []string, right []string) []string {
	out := make([]string, 0, len(left)+len(right))
	i, j := 0, 0
	for i < len(left) || j < len(right) {
		switch {
		case j >= len(right), i < len(left) && left[i] < right[j]:
			out = append(out, left[i])
			i++
		case i >= len(left), right[j] < left[i]:
			out = append(out, right[j])
			j++
		default:
			out = append(out, left[i])
			i++
			j++
		}
	}
	return out
}

func selectDriftSampleIDs(ids []string, size int) []string {
	if size <= 0 || len(ids) == 0 {
		return nil
	}
	if size >= len(ids) {
		return append([]string(nil), ids...)
	}
	if size == 1 {
		return []string{ids[len(ids)/2]}
	}
	out := make([]string, 0, size)
	previous := -1
	for i := range size {
		index := i * (len(ids) - 1) / (size - 1)
		if index == previous {
			continue
		}
		out = append(out, ids[index])
		previous = index
	}
	return out
}
