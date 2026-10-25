package policy

import (
	"fmt"
	"math/bits"
	"slices"
	"strings"
)

// MaxISOPAtoms is the maximum number of boolean predicates supported by the
// truth-table Minato-Morreale ISOP analyzer (2^16 = 65,536 points, 1024 uint64 words).
const MaxISOPAtoms = 16

// Cube represents a conjunction (product) of literals over up to MaxISOPAtoms variables.
// A bit i set in Pos means variable i must be true (x_i); a bit i set in Neg
// means variable i must be false (~x_i); neither set means variable i is don't-care.
type Cube struct {
	Pos uint32
	Neg uint32
}

// Covers reports whether the cube evaluates to true for the variable assignment mask.
func (c Cube) Covers(assignment uint32) bool {
	return (assignment&c.Pos) == c.Pos && (^assignment&c.Neg) == c.Neg
}

// Format renders a human-readable conjunction using the provided atom names.
func (c Cube) Format(atoms []string) string {
	if c.Pos == 0 && c.Neg == 0 {
		return "true"
	}
	var parts []string
	for i, name := range atoms {
		bit := uint32(1) << uint(i)
		if c.Pos&bit != 0 {
			parts = append(parts, name)
		} else if c.Neg&bit != 0 {
			parts = append(parts, "!"+name)
		}
	}
	return strings.Join(parts, " && ")
}

// TruthTable represents a boolean function over nVars (0 <= nVars <= MaxISOPAtoms)
// stored as a bitset of 1 << nVars truth values.
type TruthTable struct {
	nVars int
	words []uint64
}

// NewTruthTable allocates an all-false truth table for nVars variables.
func NewTruthTable(nVars int) TruthTable {
	if nVars < 0 || nVars > MaxISOPAtoms {
		nVars = MaxISOPAtoms
	}
	size := 1 << uint(nVars)
	nWords := (size + 63) / 64
	return TruthTable{
		nVars: nVars,
		words: make([]uint64, nWords),
	}
}

func (t TruthTable) maskLast() uint64 {
	if t.nVars >= 6 {
		return ^uint64(0)
	}
	return (uint64(1) << (1 << uint(t.nVars))) - 1
}

// IsZero reports whether the function is identically 0 (false everywhere).
func (t TruthTable) IsZero() bool {
	m := t.maskLast()
	for i, w := range t.words {
		if i == len(t.words)-1 {
			w &= m
		}
		if w != 0 {
			return false
		}
	}
	return true
}

// IsOne reports whether the function is identically 1 (true everywhere).
func (t TruthTable) IsOne() bool {
	m := t.maskLast()
	for i, w := range t.words {
		if i == len(t.words)-1 {
			if (w & m) != m {
				return false
			}
		} else if w != ^uint64(0) {
			return false
		}
	}
	return true
}

// Equal reports whether t and other represent the exact same boolean function.
func (t TruthTable) Equal(other TruthTable) bool {
	if t.nVars != other.nVars || len(t.words) != len(other.words) {
		return false
	}
	m := t.maskLast()
	for i := range t.words {
		a, b := t.words[i], other.words[i]
		if i == len(t.words)-1 {
			a &= m
			b &= m
		}
		if a != b {
			return false
		}
	}
	return true
}

// Get returns the truth value at assignment (0 <= assignment < 2^nVars).
func (t TruthTable) Get(assignment uint32) bool {
	idx := assignment >> 6
	bit := assignment & 63
	if int(idx) >= len(t.words) {
		return false
	}
	return (t.words[idx]>>bit)&1 == 1
}

// Set sets the truth value at assignment to val.
func (t TruthTable) Set(assignment uint32, val bool) {
	idx := assignment >> 6
	bit := assignment & 63
	if int(idx) >= len(t.words) {
		return
	}
	if val {
		t.words[idx] |= uint64(1) << bit
	} else {
		t.words[idx] &^= uint64(1) << bit
	}
}

func (t TruthTable) and(other TruthTable) TruthTable {
	out := NewTruthTable(t.nVars)
	for i := range out.words {
		out.words[i] = t.words[i] & other.words[i]
	}
	return out
}

func (t TruthTable) andNot(other TruthTable) TruthTable {
	out := NewTruthTable(t.nVars)
	for i := range out.words {
		out.words[i] = t.words[i] &^ other.words[i]
	}
	return out
}

func (t TruthTable) or(other TruthTable) TruthTable {
	out := NewTruthTable(t.nVars)
	for i := range out.words {
		out.words[i] = t.words[i] | other.words[i]
	}
	return out
}

// cofactors splits a truth table on its highest-index variable v = nVars - 1
// into f_{v=0} and f_{v=1}, each ofnVars - 1 variables.
func (t TruthTable) cofactors() (f0, f1 TruthTable) {
	v := t.nVars - 1
	f0 = NewTruthTable(v)
	f1 = NewTruthTable(v)
	half := uint32(1) << uint(v)
	for a := range half {
		if t.Get(a) {
			f0.Set(a, true)
		}
		if t.Get(a | half) {
			f1.Set(a, true)
		}
	}
	return f0, f1
}

// tableFromCubes evaluates a list of cubes over nVars into a TruthTable.
func tableFromCubes(nVars int, cubes []Cube) TruthTable {
	out := NewTruthTable(nVars)
	if len(cubes) == 0 {
		return out
	}
	limit := uint32(1) << uint(nVars)
	for a := range limit {
		for _, c := range cubes {
			if c.Covers(a) {
				out.Set(a, true)
				break
			}
		}
	}
	return out
}

// MinatoMorrealeISOP computes an irredundant sum-of-products (ISOP) cover for
// the boolean function interval [lower, upper] (where lower implies upper, and
// upper &^ lower represents don't-care assignments).
//
// Recurrence (Minato 1992 / Morreale 1970):
//
//	C0 = ISOP(L0 & ~U1, U0)          // cubes requiring ~x_v
//	C1 = ISOP(L1 & ~U0, U1)          // cubes requiring  x_v
//	L* = (L0 & ~eval(C0)) | (L1 & ~eval(C1))
//	C* = ISOP(L*, U0 & U1)           // cubes independent of x_v
//	return (~x_v · C0) ∪ (x_v · C1) ∪ C*
//
// Recursion depth is strictly bounded by lower.nVars <= MaxISOPAtoms (CP-01, CP-02).
func MinatoMorrealeISOP(lower, upper TruthTable) []Cube {
	if lower.IsZero() {
		return nil
	}
	if upper.IsOne() {
		return []Cube{{Pos: 0, Neg: 0}}
	}
	if lower.nVars == 0 {
		return []Cube{{Pos: 0, Neg: 0}}
	}

	v := uint(lower.nVars - 1)
	bit := uint32(1) << v

	l0, l1 := lower.cofactors()
	u0, u1 := upper.cofactors()

	c0 := MinatoMorrealeISOP(l0.andNot(u1), u0)
	c1 := MinatoMorrealeISOP(l1.andNot(u0), u1)

	cov0 := tableFromCubes(int(v), c0)
	cov1 := tableFromCubes(int(v), c1)

	lStar := l0.andNot(cov0).or(l1.andNot(cov1))
	uStar := u0.and(u1)
	cStar := MinatoMorrealeISOP(lStar, uStar)

	out := make([]Cube, 0, len(c0)+len(c1)+len(cStar))
	for _, c := range c0 {
		out = append(out, Cube{Pos: c.Pos, Neg: c.Neg | bit})
	}
	for _, c := range c1 {
		out = append(out, Cube{Pos: c.Pos | bit, Neg: c.Neg})
	}
	out = append(out, cStar...)
	return out
}

// AnalysisReport summarizes static boolean analysis of an Engine's policy set.
type AnalysisReport struct {
	// Atoms lists the extracted atomic predicates corresponding to bit positions 0..len(Atoms)-1.
	Atoms []string
	// IrredundantCover is the minimal ISOP cover of the effective allow function.
	IrredundantCover []Cube
	// RedundantPolicies lists policy IDs whose removal does not change the effective allow function.
	RedundantPolicies []string
	// ShadowedAllowPolicies lists allow policy IDs that are completely overridden by deny policies.
	ShadowedAllowPolicies []string
	// AllowedMintermCount is the number of valid predicate assignments permitted by the engine.
	AllowedMintermCount int
}

// Analyze extracts atomic predicates and mutual-exclusion constraints from the
// engine's policies, computes the effective authorization function
// (Allow_1 | ... | Allow_k) & ~(Deny_1 | ... | Deny_m) over the care set, and
// runs Minato-Morreale ISOP to report redundant/shadowed policies and a minimal
// irredundant cover.
func (e *Engine) Analyze() (AnalysisReport, error) {
	e.mu.RLock()
	policies := slices.Clone(e.policies)
	e.mu.RUnlock()

	atomIndex := make(map[string]int)
	var atoms []string
	var groups [][]int // mutual exclusion groups (at most one atom in a group can be true)

	type groupKey struct {
		category string
		field    string
	}
	exclusiveMap := make(map[groupKey][]int)

	addAtom := func(label string, excl *groupKey) (int, error) {
		if idx, ok := atomIndex[label]; ok {
			return idx, nil
		}
		if len(atoms) >= MaxISOPAtoms {
			return 0, fmt.Errorf("policy analysis exceeds %d atomic predicates", MaxISOPAtoms)
		}
		idx := len(atoms)
		atoms = append(atoms, label)
		atomIndex[label] = idx
		if excl != nil {
			exclusiveMap[*excl] = append(exclusiveMap[*excl], idx)
		}
		return idx, nil
	}

	pCubes := make([]Cube, len(policies))
	for pi, p := range policies {
		var c Cube
		if p.Principal != nil {
			if p.Principal.Type != "" && p.Principal.Type != "*" {
				idx, err := addAtom("principal.type=="+p.Principal.Type, &groupKey{"p.type", ""})
				if err != nil {
					return AnalysisReport{}, err
				}
				c.Pos |= 1 << uint(idx)
			}
			for _, role := range p.Principal.Roles {
				if role != "*" {
					idx, err := addAtom("principal.role:"+role, nil)
					if err != nil {
						return AnalysisReport{}, err
					}
					c.Pos |= 1 << uint(idx)
				}
			}
		}
		if len(p.Actions) == 1 && p.Actions[0] != "*" && !strings.HasSuffix(p.Actions[0], ":*") {
			idx, err := addAtom("action=="+p.Actions[0], &groupKey{"action", ""})
			if err != nil {
				return AnalysisReport{}, err
			}
			c.Pos |= 1 << uint(idx)
		} else if len(p.Actions) > 0 && p.Actions[0] != "*" {
			idx, err := addAtom("action in ["+strings.Join(p.Actions, ",")+"]", nil)
			if err != nil {
				return AnalysisReport{}, err
			}
			c.Pos |= 1 << uint(idx)
		}
		if p.Resource != nil {
			if p.Resource.Type != "" && p.Resource.Type != "*" {
				idx, err := addAtom("resource.type=="+p.Resource.Type, &groupKey{"r.type", ""})
				if err != nil {
					return AnalysisReport{}, err
				}
				c.Pos |= 1 << uint(idx)
			}
			if p.Resource.Owner != "" {
				idx, err := addAtom("resource.owner=="+p.Resource.Owner, &groupKey{"r.owner", ""})
				if err != nil {
					return AnalysisReport{}, err
				}
				c.Pos |= 1 << uint(idx)
			}
			if p.Resource.OrgID != "" {
				idx, err := addAtom("resource.org_id=="+p.Resource.OrgID, &groupKey{"r.org", ""})
				if err != nil {
					return AnalysisReport{}, err
				}
				c.Pos |= 1 << uint(idx)
			}
		}
		for _, cond := range p.Conditions {
			valStr := fmt.Sprintf("%v", cond.Value)
			switch cond.Operator {
			case OpEquals:
				idx, err := addAtom(cond.Field+"=="+valStr, &groupKey{"cond.eq", cond.Field})
				if err != nil {
					return AnalysisReport{}, err
				}
				c.Pos |= 1 << uint(idx)
			case OpNotEquals:
				idx, err := addAtom(cond.Field+"=="+valStr, &groupKey{"cond.eq", cond.Field})
				if err != nil {
					return AnalysisReport{}, err
				}
				c.Neg |= 1 << uint(idx)
			default:
				idx, err := addAtom(fmt.Sprintf("%s %s %s", cond.Field, cond.Operator, valStr), nil)
				if err != nil {
					return AnalysisReport{}, err
				}
				c.Pos |= 1 << uint(idx)
			}
		}
		pCubes[pi] = c
	}

	for _, g := range exclusiveMap {
		if len(g) > 1 {
			groups = append(groups, g)
		}
	}

	nVars := len(atoms)
	care := NewTruthTable(nVars)
	dontCare := NewTruthTable(nVars)
	limit := uint32(1) << uint(nVars)

	for a := range limit {
		possible := true
		for _, g := range groups {
			setCount := 0
			for _, bitIdx := range g {
				if (a>>uint(bitIdx))&1 == 1 {
					setCount++
					if setCount > 1 {
						possible = false
						break
					}
				}
			}
			if !possible {
				break
			}
		}
		if possible {
			care.Set(a, true)
		} else {
			dontCare.Set(a, true)
		}
	}

	evalSubset := func(skipIndex int) (allowTable, denyTable, effective TruthTable) {
		allowTable = NewTruthTable(nVars)
		denyTable = NewTruthTable(nVars)
		for i, p := range policies {
			if i == skipIndex {
				continue
			}
			t := tableFromCubes(nVars, []Cube{pCubes[i]}).and(care)
			if p.Effect == Deny {
				denyTable = denyTable.or(t)
			} else {
				allowTable = allowTable.or(t)
			}
		}
		effective = allowTable.andNot(denyTable)
		return allowTable, denyTable, effective
	}

	_, denyAll, effective := evalSubset(-1)

	var redundant []string
	var shadowed []string
	for i, p := range policies {
		if p.Effect == Allow {
			pTable := tableFromCubes(nVars, []Cube{pCubes[i]}).and(care)
			if !pTable.IsZero() && pTable.andNot(denyAll).IsZero() {
				shadowed = append(shadowed, p.ID)
			}
		}
		_, _, withoutI := evalSubset(i)
		if withoutI.Equal(effective) {
			redundant = append(redundant, p.ID)
		}
	}

	upper := effective.or(dontCare)
	cover := MinatoMorrealeISOP(effective, upper)

	minterms := 0
	for _, w := range effective.words {
		minterms += bits.OnesCount64(w)
	}

	return AnalysisReport{
		Atoms:                 atoms,
		IrredundantCover:      cover,
		RedundantPolicies:     redundant,
		ShadowedAllowPolicies: shadowed,
		AllowedMintermCount:   minterms,
	}, nil
}
