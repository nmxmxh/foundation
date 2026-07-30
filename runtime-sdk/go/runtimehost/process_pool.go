package runtimehost

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
)

type ProcessRequest struct {
	UnitID        string
	Input         []byte
	ContextHash   int32
	ModuleVersion int32
}

type ProcessResponse struct {
	Output      []byte
	Diagnostics string
	StatusCode  int32
	OutputEpoch int32
}

type ProcessTransportMode string

const (
	ProcessTransportAuto         ProcessTransportMode = "auto"
	ProcessTransportFFI          ProcessTransportMode = "ffi"
	ProcessTransportStdio        ProcessTransportMode = "stdio"
	ProcessTransportSharedMemory ProcessTransportMode = "shm"
)

type ProcessPoolOptions struct {
	Command         []string
	Env             []string
	Dir             string
	Workers         int
	Logger          logger.Logger
	Transport       ProcessTransportMode
	SharedMemoryDir string
	ExchangeTimeout time.Duration

	// ArenaBytes requests a shared data-plane arena per worker.
	//
	// Zero means no arena, which is the right default: most units exchange
	// control-sized payloads and an arena would be unused address space. Set it
	// for units that carry bulk data — an embedding matrix, a columnar batch —
	// which otherwise cannot cross the 1 KiB control payload at all. Clamped to
	// the tiers in runtime_shared_arena.capnp.
	//
	// Requires the shared-memory transport: the arena is a second mapping beside
	// the control buffer, so a stdio worker has nowhere to put it.
	ArenaBytes uint32
}

type ProcessPool struct {
	logger          logger.Logger
	bufferPool      sync.Pool
	allWorkers      []*processWorker
	nextWorker      atomic.Uint32
	exchangeTimeout time.Duration
	transport       ProcessTransportSupport
}

type workerExchange interface {
	Exchange(context.Context, string, []byte) error
	Close() error
	Restart() error
}

type stdioExchange struct {
	stdin  io.WriteCloser
	stdout *bufio.Reader
}

type processWorker struct {
	command []string
	env     []string
	dir     string
	index   int
	logger  logger.Logger
	mode    ProcessTransportMode
	shmDir  string
	shm     *sharedMemorySegment

	// arenaBytes is zero when this worker has no data plane.
	arenaBytes uint32
	arenaShm   *sharedMemorySegment
	arena      *Arena

	busy   atomic.Bool
	health sync.RWMutex

	restartCount uint32
	lastError    string
	lastStarted  time.Time
	lastSuccess  time.Time
	lastFailure  time.Time

	mu           sync.Mutex
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       *bufio.Reader
	testExchange workerExchange

	exchangeLoopMu   sync.Mutex
	exchangeRequests chan exchangeRequest
	exchangeResults  chan error
	exchangeStop     chan struct{}
	exchangeLoopDone chan struct{}
}

type exchangeRequest struct {
	ctx    context.Context
	unitID string
	buffer []byte
}

var errWorkerBusy = errors.New("process worker busy")

func NewProcessPool(opts ProcessPoolOptions) (*ProcessPool, error) {
	if len(opts.Command) == 0 || strings.TrimSpace(opts.Command[0]) == "" {
		return nil, errors.New("native runtime command is required")
	}
	if opts.Workers <= 0 {
		opts.Workers = defaultProcessWorkerCount(runtime.NumCPU())
	}
	if opts.Logger == nil {
		opts.Logger, _ = logger.NewDefault()
	}
	opts.Logger = opts.Logger.With("component", "runtime_process_pool")
	if opts.ExchangeTimeout <= 0 {
		opts.ExchangeTimeout = DefaultProcessExchangeTimeout
	}
	transportSupport, err := ResolveProcessTransportSupport(opts.Transport, opts.SharedMemoryDir)
	if err != nil {
		return nil, err
	}

	pool := &ProcessPool{
		logger:          opts.Logger,
		exchangeTimeout: opts.ExchangeTimeout,
		transport:       transportSupport,
		bufferPool: sync.Pool{New: func() any {
			buffer := make([]byte, generated.BUFFER_TOTAL_BYTES)
			return &buffer
		}},
	}
	if transportSupport.Fallback {
		opts.Logger.Warn("native runtime transport fallback enabled", "reason", transportSupport.Reason)
	}
	for index := 0; index < opts.Workers; index++ {
		worker := &processWorker{
			command:    append([]string(nil), opts.Command...),
			env:        append([]string(nil), opts.Env...),
			dir:        strings.TrimSpace(opts.Dir),
			index:      index + 1,
			logger:     opts.Logger.With("worker_index", index+1),
			mode:       transportSupport.Resolved,
			shmDir:     strings.TrimSpace(opts.SharedMemoryDir),
			arenaBytes: normalizeArenaBytes(opts.ArenaBytes),
		}
		if err := worker.start(); err != nil {
			_ = pool.Close()
			return nil, err
		}
		pool.allWorkers = append(pool.allWorkers, worker)
	}
	return pool, nil
}

func (p *ProcessPool) Execute(ctx context.Context, req ProcessRequest) (ProcessResponse, error) {
	return p.execute(ctx, req, nil)
}

// ExecuteInto executes a runtime unit and copies the active output into dst.
// The returned Output aliases dst and remains owned by the caller. Execute
// remains the compatibility API for callers that need a newly owned slice.
func (p *ProcessPool) ExecuteInto(ctx context.Context, req ProcessRequest, dst []byte) (ProcessResponse, error) {
	if dst == nil {
		return ProcessResponse{}, errors.New("process output destination is required")
	}
	return p.execute(ctx, req, dst)
}

// ExecuteOnWorker runs a request on one specific worker.
//
// Required whenever the request references that worker's arena. The pool
// otherwise picks a worker by availability, so a caller that staged a batch into
// worker 0's arena and then executed through the pool would, half the time, run
// on a worker whose arena is empty — the descriptors read back as FREE and the
// call fails. Staging and execution must name the same worker.
func (p *ProcessPool) ExecuteOnWorker(ctx context.Context, index int, req ProcessRequest) (ProcessResponse, error) {
	if p == nil {
		return ProcessResponse{}, errors.New("process pool is nil")
	}
	if index < 0 || index >= len(p.allWorkers) {
		return ProcessResponse{}, fmt.Errorf("worker index %d out of range (%d workers)", index, len(p.allWorkers))
	}
	return p.execute(ctx, req, nil, p.allWorkers[index])
}

func (p *ProcessPool) execute(ctx context.Context, req ProcessRequest, dst []byte, pinned ...*processWorker) (ProcessResponse, error) {
	if p == nil {
		return ProcessResponse{}, errors.New("process pool is nil")
	}
	if strings.TrimSpace(req.UnitID) == "" {
		return ProcessResponse{}, errors.New("unit id is required")
	}
	execCtx := ctx
	var cancel context.CancelFunc
	if execCtx == nil {
		execCtx = context.Background()
	}
	if _, hasDeadline := execCtx.Deadline(); !hasDeadline && p.exchangeTimeout > 0 {
		execCtx, cancel = context.WithTimeout(execCtx, p.exchangeTimeout)
		defer cancel()
	}

	rawPtr := p.bufferPool.Get().(*[]byte)
	raw := *rawPtr
	defer func() {
		*rawPtr = raw
		p.bufferPool.Put(rawPtr)
	}()
	buffer, err := NewBuffer(raw)
	if err != nil {
		return ProcessResponse{}, err
	}
	buffer.Reset()
	buffer.Initialize(req.ModuleVersion)
	if err := buffer.SetHeaderInt(generated.INT_IDX_CONTEXT_HASH, req.ContextHash); err != nil {
		return ProcessResponse{}, err
	}
	if err := buffer.SetInputBytesFast(req.Input); err != nil {
		return ProcessResponse{}, err
	}
	if _, err := buffer.AddEpoch(generated.IDX_INPUT_WRITTEN, 1); err != nil {
		return ProcessResponse{}, err
	}

	if err := p.executeOnSelectedWorker(execCtx, req, raw, pinned...); err != nil {
		return ProcessResponse{}, err
	}

	statusCode, err := buffer.HeaderInt(generated.INT_IDX_STATUS_CODE)
	if err != nil {
		return ProcessResponse{}, err
	}
	output, err := buffer.OutputBytesView()
	if err != nil {
		return ProcessResponse{}, err
	}

	ownedOutput := dst
	if ownedOutput == nil {
		ownedOutput = make([]byte, len(output))
	} else if len(ownedOutput) < len(output) {
		return ProcessResponse{}, fmt.Errorf("process output destination too small: %d < %d", len(ownedOutput), len(output))
	}
	ownedOutput = ownedOutput[:len(output)]
	copy(ownedOutput, output)
	response := ProcessResponse{
		Output:      ownedOutput,
		Diagnostics: strings.TrimSpace(buffer.DiagnosticsText()),
		StatusCode:  statusCode,
		OutputEpoch: buffer.LoadEpoch(generated.IDX_OUTPUT_WRITTEN),
	}
	_, _ = buffer.AddEpoch(generated.IDX_OUTPUT_CONSUMED, 1)

	if statusCode != 0 {
		if response.Diagnostics == "" {
			response.Diagnostics = "native runtime returned non-zero status"
		}
		return response, errors.New(response.Diagnostics)
	}
	return response, nil
}

func (p *ProcessPool) Diagnostics() ProcessPoolDiagnostics {
	if p == nil {
		return ProcessPoolDiagnostics{}
	}
	workers := make([]ProcessWorkerSnapshot, 0, len(p.allWorkers))
	for _, worker := range p.allWorkers {
		workers = append(workers, worker.snapshot())
	}
	return ProcessPoolDiagnostics{
		Transport:         p.transport,
		ExchangeTimeoutMS: int64(p.exchangeTimeout / time.Millisecond),
		Workers:           workers,
	}
}

func (p *ProcessPool) executeOnSelectedWorker(ctx context.Context, req ProcessRequest, buffer []byte, pinned ...*processWorker) error {
	if len(p.allWorkers) == 0 {
		return errors.New("process pool has no workers")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// A pinned worker bypasses selection entirely: the request references that
	// worker's arena, so any other worker would read an empty one.
	if len(pinned) > 0 && pinned[0] != nil {
		return pinned[0].execute(ctx, req.UnitID, buffer)
	}

	preferredIndex := p.preferredWorkerIndex(req.ContextHash)
	for offset := 0; offset < len(p.allWorkers); offset++ {
		index := (preferredIndex + offset) % len(p.allWorkers)
		err := p.allWorkers[index].tryExecute(ctx, req.UnitID, buffer)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, errWorkerBusy):
			continue
		default:
			return err
		}
	}
	return p.allWorkers[preferredIndex].execute(ctx, req.UnitID, buffer)
}

func (p *ProcessPool) preferredWorkerIndex(contextHash int32) int {
	if len(p.allWorkers) == 0 {
		return 0
	}
	if contextHash != 0 {
		hash := int(contextHash)
		if hash < 0 {
			hash = -hash
		}
		return hash % len(p.allWorkers)
	}
	return int(p.nextWorker.Add(1)-1) % len(p.allWorkers)
}

func (p *ProcessPool) Close() error {
	if p == nil {
		return nil
	}
	var firstErr error
	for _, worker := range p.allWorkers {
		if err := worker.close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (w *processWorker) start() error {
	w.mu.Lock()
	for range 1 { // dummy loop for defer alternative if needed, but here we just lock/unlock
	}
	defer w.mu.Unlock()
	return w.startLocked()
}

func (w *processWorker) startLocked() error {
	if w.testExchange != nil {
		return nil
	}
	if w.cmd != nil {
		return nil
	}
	env := append([]string(nil), w.env...)
	env = append(env, "OVRT_RUNTIME_TRANSPORT="+string(w.mode))
	if w.mode == ProcessTransportSharedMemory {
		if w.shm == nil {
			segment, err := newSharedMemorySegment(w.shmDir, int(generated.BUFFER_TOTAL_BYTES))
			if err != nil {
				return err
			}
			w.shm = segment
		}
		env = append(env, "OVRT_SHM_PATH="+w.shm.path)

		// The data-plane arena is a second, much larger mapping. Bulk payloads
		// live here; the control buffer carries only a descriptor id.
		if w.arenaBytes > 0 {
			if w.arenaShm == nil {
				segment, arenaErr := newSharedMemoryFile(w.shmDir, int(w.arenaBytes))
				if arenaErr != nil {
					return fmt.Errorf("create arena segment: %w", arenaErr)
				}
				staging := make([]byte, int(w.arenaBytes))
				publish := func(staged int) error {
					if staged <= 0 || staged > len(staging) {
						staged = len(staging)
					}
					return segment.WriteAt(staging[:staged], 0)
				}
				arena, arenaErr := NewArenaOverMapping(staging, publish, segment.ReadAt)
				if arenaErr != nil {
					_ = segment.Close()
					return fmt.Errorf("initialize arena: %w", arenaErr)
				}
				// Publish the freshly initialized header before any staging.
				// The arena is built in the staging buffer, so until the first
				// flush the file is all zeroes — a consumer opening it would
				// fail header validation and report no arena at all, rather
				// than an empty one.
				if arenaErr := arena.Sync(); arenaErr != nil {
					_ = segment.Close()
					return fmt.Errorf("publish arena header: %w", arenaErr)
				}
				w.arenaShm = segment
				w.arena = arena
			}
			env = append(env, "OVRT_SHM_ARENA_PATH="+w.arenaShm.path)
		}
	}

	// #nosec G204 -- runtime worker command comes from trusted ProcessPoolOptions, never request input.
	cmd := exec.Command(w.command[0], w.command[1:]...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	if w.dir != "" {
		cmd.Dir = w.dir
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	w.cmd = cmd
	w.stdin = stdin
	w.stdout = bufio.NewReader(stdoutPipe)
	w.recordStarted()
	go w.logStderr(stderrPipe)
	return nil
}

func (w *processWorker) close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closeLocked()
}

func (w *processWorker) closeLocked() error {
	w.stopExchangeLoop()
	if w.testExchange != nil {
		return w.testExchange.Close()
	}
	if w.cmd == nil {
		return nil
	}

	if w.stdin != nil {
		_ = w.stdin.Close()
	}
	if w.cmd.Process != nil {
		killErr := w.cmd.Process.Kill()
		if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			return killErr
		}
	}
	waitErr := w.cmd.Wait()

	w.cmd = nil
	w.stdin = nil
	w.stdout = nil
	if w.arenaShm != nil {
		if err := w.arenaShm.Close(); err != nil && waitErr == nil {
			waitErr = err
		}
		w.arenaShm = nil
		w.arena = nil
	}
	if w.shm != nil {
		if err := w.shm.Close(); err != nil && waitErr == nil {
			waitErr = err
		}
		w.shm = nil
	}

	if waitErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			return waitErr
		}
	}
	return nil
}

func (w *processWorker) restartLocked() error {
	if w.testExchange != nil {
		return w.testExchange.Restart()
	}
	if err := w.closeLocked(); err != nil {
		return err
	}
	return w.startLocked()
}

func (w *processWorker) execute(ctx context.Context, unitID string, buffer []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	w.busy.Store(true)
	defer w.busy.Store(false)
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.executeHeld(ctx, unitID, buffer)
}

func (w *processWorker) tryExecute(ctx context.Context, unitID string, buffer []byte) error {
	if !w.mu.TryLock() {
		return errWorkerBusy
	}
	defer w.mu.Unlock()

	w.busy.Store(true)
	defer w.busy.Store(false)
	return w.executeHeld(ctx, unitID, buffer)
}

func (w *processWorker) executeHeld(ctx context.Context, unitID string, buffer []byte) error {
	if err := w.startLocked(); err != nil {
		w.recordFailure(err)
		return err
	}
	if err := w.executeWithContext(ctx, unitID, buffer); err != nil {
		w.recordFailure(err)
		w.logger.WarnContext(ctx, "native runtime exchange failed; restarting worker", "error", err)
		w.incrementRestart()
		if restartErr := w.restartLocked(); restartErr != nil {
			w.recordFailure(restartErr)
			return fmt.Errorf("restart native runtime worker: %w", restartErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.executeWithContext(ctx, unitID, buffer); err != nil {
			w.recordFailure(err)
			return err
		}
	}
	w.recordSuccess()
	return ctx.Err()
}

func (w *processWorker) executeWithContext(ctx context.Context, unitID string, buffer []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	requests, results := w.ensureExchangeLoop()
	request := exchangeRequest{ctx: ctx, unitID: unitID, buffer: buffer}
	select {
	case requests <- request:
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-results:
		return err
	case <-ctx.Done():
		w.logger.WarnContext(ctx, "native runtime exchange timed out; terminating worker", "error", ctx.Err())
		if w.cmd != nil && w.cmd.Process != nil {
			_ = w.cmd.Process.Kill()
		}
		err := <-results
		if err == nil {
			return ctx.Err()
		}
		return errors.Join(ctx.Err(), err)
	}
}

// ensureExchangeLoop starts one bounded exchange goroutine for the worker.
// Worker execution is serialized by mu, so capacity one provides backpressure
// without allocating request-specific goroutines or channels.
func (w *processWorker) ensureExchangeLoop() (chan<- exchangeRequest, <-chan error) {
	w.exchangeLoopMu.Lock()
	defer w.exchangeLoopMu.Unlock()
	if w.exchangeRequests != nil {
		return w.exchangeRequests, w.exchangeResults
	}
	w.exchangeRequests = make(chan exchangeRequest, 1)
	w.exchangeResults = make(chan error, 1)
	w.exchangeStop = make(chan struct{})
	w.exchangeLoopDone = make(chan struct{})
	go w.runExchangeLoop(w.exchangeRequests, w.exchangeResults, w.exchangeStop, w.exchangeLoopDone)
	return w.exchangeRequests, w.exchangeResults
}

func (w *processWorker) runExchangeLoop(requests <-chan exchangeRequest, results chan<- error, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case request := <-requests:
			results <- w.exchange().Exchange(request.ctx, request.unitID, request.buffer)
		case <-stop:
			return
		}
	}
}

func (w *processWorker) stopExchangeLoop() {
	w.exchangeLoopMu.Lock()
	if w.exchangeRequests == nil {
		w.exchangeLoopMu.Unlock()
		return
	}
	stop := w.exchangeStop
	done := w.exchangeLoopDone
	w.exchangeRequests = nil
	w.exchangeResults = nil
	w.exchangeStop = nil
	w.exchangeLoopDone = nil
	close(stop)
	w.exchangeLoopMu.Unlock()
	<-done
}

func (w *processWorker) exchange() workerExchange {
	if w.testExchange != nil {
		return w.testExchange
	}
	// Honour the resolved transport. This used to return stdioExchange
	// unconditionally, which quietly disabled the shared-memory transport for
	// every caller: Execute routes through the exchange loop, so the shm branch
	// in executeLocked had no production caller and a pool configured for shm
	// still copied its control buffer over stdio on every exchange. The bug was
	// invisible because stdio is correct — just slower, and unable to carry an
	// arena handle.
	if w.mode == ProcessTransportSharedMemory {
		// Deliberately not falling back to stdio when the segment is missing.
		// The child was launched with OVRT_RUNTIME_TRANSPORT=shm and is waiting
		// on the mapping, so stdio would hang rather than degrade; a clear error
		// from Exchange is the honest outcome.
		return sharedMemoryExchange{stdin: w.stdin, stdout: w.stdout, shm: w.shm}
	}
	return stdioExchange{stdin: w.stdin, stdout: w.stdout}
}

// sharedMemoryExchange swaps the control buffer through a shared mapping and
// uses stdio only to signal the unit id and await an acknowledgement.
//
// The buffer never travels over the pipe, so the exchange cost is independent of
// its size — which is what makes an arena handle in the control buffer useful.
type sharedMemoryExchange struct {
	stdin  io.WriteCloser
	stdout *bufio.Reader
	shm    *sharedMemorySegment
}

func (x sharedMemoryExchange) Exchange(_ context.Context, unitID string, buffer []byte) error {
	if x.shm == nil {
		return errors.New("shared memory segment is not initialized")
	}
	if len(buffer) > len(x.shm.raw) {
		return fmt.Errorf("control buffer is %d bytes, shared segment is %d", len(buffer), len(x.shm.raw))
	}
	copy(x.shm.raw, buffer)
	if err := writeStringFrame(x.stdin, unitID); err != nil {
		return err
	}
	ack, err := readFrame(x.stdout)
	if err != nil {
		return err
	}
	if len(ack) > 0 {
		return fmt.Errorf("unexpected shared memory ack payload: %s", string(ack))
	}
	copy(buffer, x.shm.raw)
	return nil
}

func (x sharedMemoryExchange) Close() error {
	if x.stdin != nil {
		return x.stdin.Close()
	}
	return nil
}

func (x sharedMemoryExchange) Restart() error {
	return nil
}

func (x stdioExchange) Exchange(_ context.Context, unitID string, buffer []byte) error {
	if err := writeStringFrame(x.stdin, unitID); err != nil {
		return err
	}
	if err := writeFrame(x.stdin, buffer); err != nil {
		return err
	}
	return readFrameInto(x.stdout, buffer)
}

func (x stdioExchange) Close() error {
	if x.stdin != nil {
		return x.stdin.Close()
	}
	return nil
}

func (x stdioExchange) Restart() error {
	return nil
}

func (w *processWorker) logStderr(stderr io.ReadCloser) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		w.logger.Warn("native runtime stderr", "line", scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		w.logger.Warn("native runtime stderr scan failed", "error", err)
	}
}

func writeFrame(w io.Writer, payload []byte) error {
	frameSize, err := checkedFrameSize(len(payload))
	if err != nil {
		return err
	}
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], frameSize)
	if _, err := w.Write(size[:]); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

func writeStringFrame(w io.Writer, payload string) error {
	frameSize, err := checkedFrameSize(len(payload))
	if err != nil {
		return err
	}
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], frameSize)
	if _, err := w.Write(size[:]); err != nil {
		return err
	}
	_, err = io.WriteString(w, payload)
	return err
}

func checkedFrameSize(size int) (uint32, error) {
	if size < 0 || size > math.MaxUint32 {
		return 0, fmt.Errorf("frame too large: %d", size)
	}
	// #nosec G115 -- guarded by MaxUint32 check above.
	return uint32(size), nil
}

func readFrame(r io.Reader) ([]byte, error) {
	length, err := readFrameLength(r)
	if err != nil {
		return nil, err
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func readFrameInto(r io.Reader, dst []byte) error {
	length, err := readFrameLength(r)
	if err != nil {
		return err
	}
	if uint64(length) != uint64(len(dst)) {
		return fmt.Errorf("unexpected runtime buffer length: %d != %d", length, len(dst))
	}
	_, err = io.ReadFull(r, dst)
	return err
}

func readFrameLength(r io.Reader) (uint32, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(size[:]), nil
}

func defaultProcessWorkerCount(cores int) int {
	switch {
	case cores <= 1:
		return 1
	case cores <= 4:
		return 2
	case cores <= 8:
		return 4
	case cores <= 16:
		return 8
	default:
		return 12
	}
}

func resolveProcessTransportMode(requested ProcessTransportMode, sharedMemoryDir string) (ProcessTransportMode, error) {
	support, err := ResolveProcessTransportSupport(requested, sharedMemoryDir)
	if err != nil {
		return "", err
	}
	return support.Resolved, nil
}

func normalizeProcessTransportMode(mode ProcessTransportMode) ProcessTransportMode {
	switch ProcessTransportMode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case "", ProcessTransportAuto:
		return ProcessTransportAuto
	case ProcessTransportFFI:
		return ProcessTransportFFI
	case ProcessTransportStdio:
		return ProcessTransportStdio
	case ProcessTransportSharedMemory:
		return ProcessTransportSharedMemory
	default:
		return mode
	}
}

func (w *processWorker) recordStarted() {
	w.health.Lock()
	defer w.health.Unlock()
	w.lastStarted = time.Now().UTC()
}

func (w *processWorker) recordSuccess() {
	w.health.Lock()
	defer w.health.Unlock()
	w.lastSuccess = time.Now().UTC()
	w.lastError = ""
}

func (w *processWorker) recordFailure(err error) {
	if err == nil {
		return
	}
	w.health.Lock()
	defer w.health.Unlock()
	w.lastFailure = time.Now().UTC()
	w.lastError = err.Error()
}

func (w *processWorker) incrementRestart() {
	w.health.Lock()
	defer w.health.Unlock()
	w.restartCount++
}

func (w *processWorker) snapshot() ProcessWorkerSnapshot {
	w.health.RLock()
	defer w.health.RUnlock()
	return ProcessWorkerSnapshot{
		Index:        w.index,
		Mode:         w.mode,
		Busy:         w.busy.Load(),
		RestartCount: w.restartCount,
		LastError:    w.lastError,
		LastStarted:  w.lastStarted,
		LastSuccess:  w.lastSuccess,
		LastFailure:  w.lastFailure,
	}
}

// normalizeArenaBytes clamps a requested arena size to the tiers the shared
// arena contract defines. Zero passes through as "no data plane".
//
// Clamped rather than rejected: a caller asking for more than the maximum wants
// as much as it can get, and failing pool construction over a sizing hint would
// take down a service that would otherwise run.
func normalizeArenaBytes(requested uint32) uint32 {
	if requested == 0 {
		return 0
	}
	if requested < generated.ARENA_MIN_BYTES {
		return generated.ARENA_MIN_BYTES
	}
	if requested > generated.ARENA_MAX_BYTES {
		return generated.ARENA_MAX_BYTES
	}
	return requested
}

// Arena exposes a worker's data plane, for tests and for callers that write
// batches before dispatching. Nil when the worker has no arena.
func (w *processWorker) Arena() *Arena { return w.arena }

// WorkerArena returns a worker's data-plane arena, or nil when the pool has none.
//
// Exposed so a caller can stage a batch into the same region the worker will
// read. Callers must serialise their own use of a given arena: it is a bump
// allocator reset per exchange, so two concurrent stagings into one arena would
// overwrite each other.
func (p *ProcessPool) WorkerArena(index int) *Arena {
	if p == nil || index < 0 || index >= len(p.allWorkers) {
		return nil
	}
	worker := p.allWorkers[index]
	worker.mu.Lock()
	defer worker.mu.Unlock()
	// The arena is created when the worker process starts, so ensure it has.
	if err := worker.startLocked(); err != nil {
		return nil
	}
	return worker.arena
}
