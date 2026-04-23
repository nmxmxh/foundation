package runtimehost

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	"go.uber.org/zap"
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
}

type ProcessPool struct {
	logger          logger.Logger
	bufferPool      sync.Pool
	allWorkers      []*processWorker
	nextWorker      atomic.Uint32
	exchangeTimeout time.Duration
	transport       ProcessTransportSupport
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

	busy   atomic.Bool
	health sync.RWMutex

	restartCount uint32
	lastError    string
	lastStarted  time.Time
	lastSuccess  time.Time
	lastFailure  time.Time

	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
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
		opts.Logger = opts.Logger.With(zap.String("component", "runtime_process_pool"))
	}
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
			return make([]byte, generated.BUFFER_TOTAL_BYTES)
		}},
	}
	if transportSupport.Fallback {
		opts.Logger.Warn("native runtime transport fallback enabled", zap.String("reason", transportSupport.Reason))
	}
	for index := 0; index < opts.Workers; index++ {
		worker := &processWorker{
			command: append([]string(nil), opts.Command...),
			env:     append([]string(nil), opts.Env...),
			dir:     strings.TrimSpace(opts.Dir),
			index:   index + 1,
			logger:  opts.Logger.With(zap.Int("worker_index", index+1)),
			mode:    transportSupport.Resolved,
			shmDir:  strings.TrimSpace(opts.SharedMemoryDir),
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

	raw := p.bufferPool.Get().([]byte)
	defer p.bufferPool.Put(raw)
	buffer, err := NewBuffer(raw)
	if err != nil {
		return ProcessResponse{}, err
	}
	buffer.Reset()
	if err := buffer.Initialize(req.ModuleVersion); err != nil {
		return ProcessResponse{}, err
	}
	if err := buffer.SetHeaderInt(generated.INT_IDX_CONTEXT_HASH, req.ContextHash); err != nil {
		return ProcessResponse{}, err
	}
	if err := buffer.SetInputBytes(req.Input); err != nil {
		return ProcessResponse{}, err
	}
	if _, err := buffer.AddEpoch(generated.IDX_INPUT_WRITTEN, 1); err != nil {
		return ProcessResponse{}, err
	}

	if err := p.executeOnSelectedWorker(execCtx, req, raw); err != nil {
		return ProcessResponse{}, err
	}

	statusCode, err := buffer.HeaderInt(generated.INT_IDX_STATUS_CODE)
	if err != nil {
		return ProcessResponse{}, err
	}
	output, err := buffer.OutputBytes()
	if err != nil {
		return ProcessResponse{}, err
	}

	response := ProcessResponse{
		Output:      append([]byte(nil), output...),
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

func (p *ProcessPool) executeOnSelectedWorker(ctx context.Context, req ProcessRequest, buffer []byte) error {
	if len(p.allWorkers) == 0 {
		return errors.New("process pool has no workers")
	}
	if err := ctx.Err(); err != nil {
		return err
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
	for i := 0; i < 1; i++ { // dummy loop for defer alternative if needed, but here we just lock/unlock
	}
	defer w.mu.Unlock()
	return w.startLocked()
}

func (w *processWorker) startLocked() error {
	if w.cmd != nil {
		return nil
	}
	env := append([]string(nil), w.env...)
	env = append(env, "OVRT_RUNTIME_TRANSPORT="+string(w.mode))
	if w.mode == ProcessTransportSharedMemory {
		if w.shm == nil {
			segment, err := newSharedMemorySegment(w.shmDir)
			if err != nil {
				return err
			}
			w.shm = segment
		}
		env = append(env, "OVRT_SHM_PATH="+w.shm.path)
	}

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
		w.logger.Warn("native runtime exchange failed; restarting worker", zap.Error(err))
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
		return w.executeLocked(unitID, buffer)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	result := make(chan error, 1)
	go func() {
		result <- w.executeLocked(unitID, buffer)
	}()

	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		w.logger.Warn("native runtime exchange timed out; terminating worker", zap.Error(ctx.Err()))
		if w.cmd != nil && w.cmd.Process != nil {
			_ = w.cmd.Process.Kill()
		}
		err := <-result
		if err == nil {
			return ctx.Err()
		}
		return errors.Join(ctx.Err(), err)
	}
}

func (w *processWorker) executeLocked(unitID string, buffer []byte) error {
	if w.mode == ProcessTransportSharedMemory {
		return w.executeSharedMemoryLocked(unitID, buffer)
	}
	if err := writeFrame(w.stdin, []byte(unitID)); err != nil {
		return err
	}
	if err := writeFrame(w.stdin, buffer); err != nil {
		return err
	}
	updated, err := readFrame(w.stdout)
	if err != nil {
		return err
	}
	if len(updated) != len(buffer) {
		return fmt.Errorf("unexpected runtime buffer length: %d != %d", len(updated), len(buffer))
	}
	copy(buffer, updated)
	return nil
}

func (w *processWorker) executeSharedMemoryLocked(unitID string, buffer []byte) error {
	if w.shm == nil {
		return errors.New("shared memory segment is not initialized")
	}
	copy(w.shm.raw, buffer)
	if err := writeFrame(w.stdin, []byte(unitID)); err != nil {
		return err
	}
	ack, err := readFrame(w.stdout)
	if err != nil {
		return err
	}
	if len(ack) > 0 {
		return fmt.Errorf("unexpected shared memory ack payload: %s", string(ack))
	}
	copy(buffer, w.shm.raw)
	return nil
}

func (w *processWorker) logStderr(stderr io.ReadCloser) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		w.logger.Warn("native runtime stderr", zap.String("line", scanner.Text()))
	}
}

func writeFrame(w io.Writer, payload []byte) error {
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], uint32(len(payload)))
	if _, err := w.Write(size[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readFrame(r io.Reader) ([]byte, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return nil, err
	}
	payload := make([]byte, binary.LittleEndian.Uint32(size[:]))
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
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
