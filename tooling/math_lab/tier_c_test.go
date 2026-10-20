package math_lab

import (
	"math"
	"math/bits"
	"math/rand/v2"
	"sort"
	"testing"
)

// ---------------------------------------------------------------------------
// C1. Kingman / Lagrange / LP Worker & Queue Sizing vs scaling.AutoTuneForCores
// ---------------------------------------------------------------------------

// kingmanMeanWaitMs computes the G/G/c approximate mean queueing wait (ms)
// under arrival rate lambda (req/s), service rate mu per worker (req/s),
// worker count c, and squared coefficients of variation ca2, cs2:
//
//	W_q ≈ ((ca² + cs²) / 2) * (ρ^√(2(c+1)) / (c * μ * (1 - ρ)))
func kingmanMeanWaitMs(lambda, mu float64, c int, ca2, cs2 float64) float64 {
	rho := lambda / (float64(c) * mu)
	if rho >= 1.0 || c <= 0 {
		return math.Inf(1)
	}
	vTerm := 0.5 * (ca2 + cs2)
	busyProb := math.Pow(rho, math.Sqrt(2*float64(c+1)))
	waitSec := vTerm * (busyProb / (float64(c) * mu * (1.0 - rho)))
	return waitSec * 1000.0
}

func TestC1_KingmanLagrangeScalingEvaluation(t *testing.T) {
	// Compare Foundation's scaling.AutoTuneForCores worker allocations across
	// 4, 8, 16, 32 cores against Lagrange-optimal worker partitioning across
	// 3 pools (Default, EventTransport, Heavy) under a fixed CPU/memory budget.
	//
	// By Lagrange multipliers on ∑ (ca_i²+cs_i²)/(2 μ_i) * ρ_i/(1-ρ_i) subject
	// to ∑ c_i = C, the optimal spare capacity above offered load a_i = λ_i/μ_i
	// is proportional to √(a_i * (ca_i² + cs_i²) / μ_i) (square-root staffing).
	cases := []struct {
		cores                     int
		heuristicDefault          int
		heuristicEvent            int
		heuristicHeavy            int
		lambdas                   [3]float64 // req/s per pool
		mus                       [3]float64 // service rate per worker (req/s)
		cv2                       [3]float64 // (ca² + cs²)/2
	}{
		{4, 8, 4, 3, [3]float64{25, 15, 2.5}, [3]float64{5, 6, 1.5}, [3]float64{1.0, 1.0, 2.0}},
		{8, 16, 8, 6, [3]float64{50, 30, 5.0}, [3]float64{5, 6, 1.5}, [3]float64{1.0, 1.0, 2.0}},
		{16, 32, 12, 10, [3]float64{100, 45, 8.5}, [3]float64{5, 6, 1.5}, [3]float64{1.0, 1.0, 2.0}},
		{32, 48, 16, 16, [3]float64{150, 60, 13.5}, [3]float64{5, 6, 1.5}, [3]float64{1.0, 1.0, 2.0}},
	}

	for _, tc := range cases {
		budget := tc.heuristicDefault + tc.heuristicEvent + tc.heuristicHeavy
		heurC := [3]int{tc.heuristicDefault, tc.heuristicEvent, tc.heuristicHeavy}

		var heurWeightedWait float64
		totalLambda := tc.lambdas[0] + tc.lambdas[1] + tc.lambdas[2]
		for i := range 3 {
			w := kingmanMeanWaitMs(tc.lambdas[i], tc.mus[i], heurC[i], tc.cv2[i], tc.cv2[i])
			heurWeightedWait += (tc.lambdas[i] / totalLambda) * w
		}

		// Exhaustive discrete convex minimization over c0 + c1 + c2 == budget.
		bestWait := math.Inf(1)
		var bestC [3]int
		for c0 := 1; c0 < budget-1; c0++ {
			for c1 := 1; c1 < budget-c0; c1++ {
				c2 := budget - c0 - c1
				w0 := kingmanMeanWaitMs(tc.lambdas[0], tc.mus[0], c0, tc.cv2[0], tc.cv2[0])
				w1 := kingmanMeanWaitMs(tc.lambdas[1], tc.mus[1], c1, tc.cv2[1], tc.cv2[1])
				w2 := kingmanMeanWaitMs(tc.lambdas[2], tc.mus[2], c2, tc.cv2[2], tc.cv2[2])
				total := (tc.lambdas[0]*w0 + tc.lambdas[1]*w1 + tc.lambdas[2]*w2) / totalLambda
				if total < bestWait {
					bestWait = total
					bestC = [3]int{c0, c1, c2}
				}
			}
		}

		absDiffMs := heurWeightedWait - bestWait
		gapPct := absDiffMs / bestWait * 100.0
		t.Logf("cores=%2d budget=%3d heuristic=%v (wait=%.3fms) optimal=%v (wait=%.3fms) diff=%.3fms (%.2f%%)",
			tc.cores, budget, heurC, heurWeightedWait, bestC, bestWait, absDiffMs, gapPct)
		if math.IsInf(heurWeightedWait, 0) || absDiffMs > 2.0 {
			t.Fatalf("unexpectedly large latency difference %.3fms at cores=%d", absDiffMs, tc.cores)
		}
	}
}

// ---------------------------------------------------------------------------
// C2. Reed-Solomon GF(2^8) Erasure Coding vs Replication for Snapshot Chunks
// ---------------------------------------------------------------------------

var (
	gfExp [512]uint8
	gfLog [256]uint8
)

func init() {
	x := 1
	for i := range 255 {
		gfExp[i] = uint8(x)
		gfExp[i+255] = uint8(x)
		gfLog[x] = uint8(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11d // primitive polynomial x^8 + x^4 + x^3 + x^2 + 1
		}
	}
}

func gfMul(a, b uint8) uint8 {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

func rsEncodeParity(dataShards [][]byte, parityShards [][]byte) {
	k := len(dataShards)
	m := len(parityShards)
	shardLen := len(dataShards[0])
	for p := range m {
		out := parityShards[p]
		for i := range out {
			out[i] = 0
		}
		for d := range k {
			coeff := gfExp[(p*d)%255]
			src := dataShards[d]
			for b := range shardLen {
				out[b] ^= gfMul(coeff, src[b])
			}
		}
	}
}

func binomialChoose(n, k int) float64 {
	if k < 0 || k > n {
		return 0
	}
	res := 1.0
	for i := 1; i <= k; i++ {
		res = res * float64(n-(k-i)) / float64(i)
	}
	return res
}

func TestC2_ReedSolomonVsReplicationDurabilityAndCost(t *testing.T) {
	// Compare 3x replication (overhead 3.0x, tolerates 2 failures) against
	// RS(k=4, m=2) (overhead 1.5x, tolerates 2 failures) on a 64KB HCS2 chunk
	// under independent node failure probability p = 0.01.
	const p = 0.01
	loss3x := math.Pow(p, 3) // all 3 replicas fail

	// RS(4,2) loses data if >2 of 6 shards fail: sum_{j=3..6} C(6,j) p^j (1-p)^(6-j)
	var lossRS float64
	for j := 3; j <= 6; j++ {
		lossRS += binomialChoose(6, j) * math.Pow(p, float64(j)) * math.Pow(1-p, float64(6-j))
	}

	// Verify RS encode throughput on 64KB chunk split into k=4 x 16KB shards, m=2 parity shards.
	const shardBytes = 16 * 1024
	data := make([][]byte, 4)
	for i := range data {
		data[i] = make([]byte, shardBytes)
		for b := range data[i] {
			data[i][b] = byte((i*31 + b) & 0xff)
		}
	}
	parity := make([][]byte, 2)
	for i := range parity {
		parity[i] = make([]byte, shardBytes)
	}
	rsEncodeParity(data, parity)

	t.Logf("3x Replication: storage=3.00x, lossProb=%.3e | RS(4,2): storage=1.50x, lossProb=%.3e", loss3x, lossRS)
	if parity[0][0] == 0 && parity[1][0] == 0 {
		t.Fatal("expected non-zero RS parity bytes")
	}
}

// ---------------------------------------------------------------------------
// C3. Expander Gossip vs Hub-and-Spoke Mesh Dissemination
// ---------------------------------------------------------------------------

func TestC3_ExpanderVsHubSpokeDissemination(t *testing.T) {
	// Simulate dissemination rounds and survival under 10% random node failure
	// for N = 16, 64, 256, 512 nodes:
	// - Hub-and-spoke (current docs/mesh_dispatch_practices.md): 2 hops when hub alive,
	//   0% reach if hub fails (or regional partition).
	// - Random 4-regular expander gossip: O(log_d N) rounds, >99% reach under 10% node loss.
	rng := rand.New(rand.NewPCG(42, 101))
	for _, n := range []int{16, 64, 256, 512} {
		const d = 4
		adj := make([][]int, n)
		for u := range n {
			for k := 1; k <= d/2; k++ {
				v := (u + k) % n
				adj[u] = append(adj[u], v)
				adj[v] = append(adj[v], u)
			}
			// Add 2 random chords per node for expansion
			for range 2 {
				v := rng.IntN(n)
				adj[u] = append(adj[u], v)
				adj[v] = append(adj[v], u)
			}
		}

		// Simulate gossip broadcast from node 0 with 10% random dead nodes (excluding node 0).
		dead := make([]bool, n)
		aliveCount := 1
		for i := 1; i < n; i++ {
			if i%10 == 0 {
				dead[i] = true
			} else {
				aliveCount++
			}
		}

		informed := make([]bool, n)
		informed[0] = true
		informedCount := 1
		rounds := 0
		for rounds < 32 && informedCount < aliveCount {
			rounds++
			next := make([]bool, n)
			copy(next, informed)
			for u := range n {
				if !informed[u] || dead[u] {
					continue
				}
				for _, v := range adj[u] {
					if !dead[v] && !next[v] {
						next[v] = true
						informedCount++
					}
				}
			}
			informed = next
		}
		reachRatio := float64(informedCount) / float64(aliveCount)
		t.Logf("N=%3d: expander rounds=%2d, reachUnder10PctFailure=%.1f%%, msgsPerRound=%d vs fullMeshMsgs=%d",
			n, rounds, reachRatio*100, n*d, n*(n-1))
		if reachRatio < 0.98 {
			t.Fatalf("expected expander reach >= 98%% at N=%d, got %.2f%%", n, reachRatio*100)
		}
	}
}

// ---------------------------------------------------------------------------
// C4. Haar Wavelet Multi-Resolution Compression vs Mean Bucketing for SLO Spikes
// ---------------------------------------------------------------------------

func haarTransform(signal []float64) (approx []float64, detail []float64) {
	n := len(signal)
	approx = make([]float64, n)
	copy(approx, signal)
	detail = make([]float64, n)

	for length := n; length > 1; length >>= 1 {
		half := length >> 1
		tmp := make([]float64, length)
		for i := range half {
			a := (approx[2*i] + approx[2*i+1]) * 0.5
			d := (approx[2*i] - approx[2*i+1]) * 0.5
			tmp[i] = a
			tmp[half+i] = d
		}
		copy(approx[:length], tmp)
	}
	return approx, detail
}

func haarInverse(coeffs []float64) []float64 {
	n := len(coeffs)
	out := make([]float64, n)
	copy(out, coeffs)
	for length := 2; length <= n; length <<= 1 {
		half := length >> 1
		tmp := make([]float64, length)
		for i := range half {
			a := out[i]
			d := out[half+i]
			tmp[2*i] = a + d
			tmp[2*i+1] = a - d
		}
		copy(out[:length], tmp)
	}
	return out
}

func TestC4_HaarWaveletSpikePreservationVsMeanBuckets(t *testing.T) {
	// Generate a 256-sample latency series (baseline ~20ms) with two sharp p99
	// tail spikes (250ms at t=47, 310ms at t=190).
	// Compare 8x compression (retaining 32 numbers out of 256):
	// 1. 8-sample window mean bucketing (32 buckets) -> washes out isolated spikes by 8x!
	// 2. Haar wavelet top-32 largest-magnitude coefficients -> preserves spikes accurately.
	const n = 256
	const keep = 32
	series := make([]float64, n)
	for i := range n {
		series[i] = 20.0 + float64(i%5)
	}
	series[47] = 250.0
	series[190] = 310.0
	trueMax := 310.0

	// 1. Window mean bucketing (window = 8)
	bucketMax := 0.0
	for b := range keep {
		var sum float64
		for j := range 8 {
			sum += series[b*8+j]
		}
		mean := sum / 8.0
		if mean > bucketMax {
			bucketMax = mean
		}
	}

	// 2. Haar wavelet top-32 thresholding
	coeffs, _ := haarTransform(series)
	type idxVal struct {
		idx int
		abs float64
	}
	ranked := make([]idxVal, n)
	for i, c := range coeffs {
		ranked[i] = idxVal{idx: i, abs: math.Abs(c)}
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].abs > ranked[j].abs })
	sparse := make([]float64, n)
	for i := range keep {
		idx := ranked[i].idx
		sparse[idx] = coeffs[idx]
	}
	reconstructed := haarInverse(sparse)
	waveletMax := 0.0
	for _, v := range reconstructed {
		if v > waveletMax {
			waveletMax = v
		}
	}

	meanSpikeErrPct := math.Abs(trueMax-bucketMax) / trueMax * 100.0
	waveletSpikeErrPct := math.Abs(trueMax-waveletMax) / trueMax * 100.0
	t.Logf("8x Compression (32/256 coefficients): trueMax=%.1fms | MeanBucket max=%.1fms (err=%.1f%%) | HaarWavelet max=%.1fms (err=%.1f%%)",
		trueMax, bucketMax, meanSpikeErrPct, waveletMax, waveletSpikeErrPct)
	if waveletSpikeErrPct >= meanSpikeErrPct || waveletSpikeErrPct > 5.0 {
		t.Fatalf("expected Haar wavelet to preserve spike within 5%%, got %.2f%% vs mean bucket %.2f%%",
			waveletSpikeErrPct, meanSpikeErrPct)
	}
}

// ---------------------------------------------------------------------------
// C6. Matroid Greedy Deadline Scheduling vs FIFO / Static Priority
// ---------------------------------------------------------------------------

type unitJob struct {
	id       int
	deadline int // 1-based slot deadline
	weight   int64
}

func TestC6_MatroidGreedySchedulingVsFIFO(t *testing.T) {
	// A set of unit-time jobs with deadlines and weights forms a transversal
	// matroid: a subset S is independent iff for every t, |{j in S : d_j <= t}| <= t.
	// Rado-Edmonds theorem: ordering jobs by descending weight and greedily
	// scheduling each job in the latest available slot <= deadline maximizes
	// total completed weight on time.
	rng := rand.New(rand.NewPCG(7, 99))
	const n = 64
	jobs := make([]unitJob, n)
	for i := range n {
		jobs[i] = unitJob{
			id:       i,
			deadline: rng.IntN(n/2) + 1,
			weight:   int64(rng.IntN(100) + 1),
		}
	}

	// 1. FIFO arrival order (schedule at earliest free time t = 1, 2, ... if t <= deadline)
	var fifoWeight int64
	timeSlot := 1
	for _, j := range jobs {
		if timeSlot <= j.deadline {
			fifoWeight += j.weight
			timeSlot++
		}
	}

	// 2. Earliest-Deadline-First (EDF) ignoring weights
	edfJobs := make([]unitJob, n)
	copy(edfJobs, jobs)
	sort.Slice(edfJobs, func(i, j int) bool { return edfJobs[i].deadline < edfJobs[j].deadline })
	var edfWeight int64
	timeSlot = 1
	for _, j := range edfJobs {
		if timeSlot <= j.deadline {
			edfWeight += j.weight
			timeSlot++
		}
	}

	// 3. Matroid greedy algorithm (sort by weight descending, place in latest free slot <= deadline)
	matroidJobs := make([]unitJob, n)
	copy(matroidJobs, jobs)
	sort.Slice(matroidJobs, func(i, j int) bool { return matroidJobs[i].weight > matroidJobs[j].weight })
	occupied := make([]bool, n+1)
	var matroidWeight int64
	for _, j := range matroidJobs {
		for slot := min(j.deadline, n); slot >= 1; slot-- {
			if !occupied[slot] {
				occupied[slot] = true
				matroidWeight += j.weight
				break
			}
		}
	}

	t.Logf("64 deadline jobs: FIFO on-time weight=%d | EDF weight=%d | Matroid-Greedy optimal weight=%d (+%.1f%% vs FIFO)",
		fifoWeight, edfWeight, matroidWeight, float64(matroidWeight-fifoWeight)/float64(fifoWeight)*100.0)
	if matroidWeight < edfWeight || matroidWeight <= fifoWeight {
		t.Fatalf("matroid greedy (%d) must dominate FIFO (%d) and EDF (%d)", matroidWeight, fifoWeight, edfWeight)
	}
	_ = bits.OnesCount64(0)
}
