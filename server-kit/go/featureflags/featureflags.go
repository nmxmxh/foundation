// Package featureflags provides a structured feature flag system with rollout controls.
// It supports percentage-based rollouts, user targeting, and environment-based overrides.
//
// Usage:
//
//	flags := featureflags.New(featureflags.Config{
//	    Source: featureflags.NewEnvSource(),
//	})
//
//	if flags.IsEnabled(ctx, "new-checkout-flow", featureflags.WithUser(userID)) {
//	    // new checkout flow
//	}
package featureflags

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Flag represents a feature flag configuration.
type Flag struct {
	// Name is the unique identifier for the flag.
	Name string `json:"name"`

	// Enabled is the default state of the flag.
	Enabled bool `json:"enabled"`

	// RolloutPercentage is the percentage of users who should see the feature (0-100).
	RolloutPercentage int `json:"rollout_percentage,omitempty"`

	// AllowedUsers is a list of user IDs who should always see the feature.
	AllowedUsers []string `json:"allowed_users,omitempty"`

	// DeniedUsers is a list of user IDs who should never see the feature.
	DeniedUsers []string `json:"denied_users,omitempty"`

	// AllowedOrgs is a list of organization IDs who should always see the feature.
	AllowedOrgs []string `json:"allowed_orgs,omitempty"`

	// Environments lists the environments where this flag is active.
	// Empty means all environments.
	Environments []string `json:"environments,omitempty"`

	// StartTime is when the flag becomes active (optional).
	StartTime *time.Time `json:"start_time,omitempty"`

	// EndTime is when the flag becomes inactive (optional).
	EndTime *time.Time `json:"end_time,omitempty"`

	// Metadata contains additional flag metadata.
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// EvaluationContext holds context for evaluating a feature flag.
type EvaluationContext struct {
	UserID      string
	OrgID       string
	Environment string
	Attributes  map[string]interface{}
}

// Option is a function that modifies EvaluationContext.
type Option func(*EvaluationContext)

// WithUser sets the user ID for evaluation.
func WithUser(userID string) Option {
	return func(ctx *EvaluationContext) {
		ctx.UserID = userID
	}
}

// WithOrg sets the organization ID for evaluation.
func WithOrg(orgID string) Option {
	return func(ctx *EvaluationContext) {
		ctx.OrgID = orgID
	}
}

// WithEnvironment sets the environment for evaluation.
func WithEnvironment(env string) Option {
	return func(ctx *EvaluationContext) {
		ctx.Environment = env
	}
}

// WithAttribute sets a custom attribute for evaluation.
func WithAttribute(key string, value interface{}) Option {
	return func(ctx *EvaluationContext) {
		if ctx.Attributes == nil {
			ctx.Attributes = make(map[string]interface{})
		}
		ctx.Attributes[key] = value
	}
}

// Source is an interface for loading feature flags.
type Source interface {
	// Load returns all feature flags.
	Load(ctx context.Context) (map[string]Flag, error)
	// Watch returns a channel that receives updates when flags change.
	Watch(ctx context.Context) <-chan map[string]Flag
}

// Config holds configuration for the feature flag manager.
type Config struct {
	// Source is the flag source (env, file, remote, etc.).
	Source Source

	// DefaultEnvironment is used when none is specified.
	DefaultEnvironment string

	// RefreshInterval is how often to refresh flags from source.
	RefreshInterval time.Duration

	// OnChange is called when flags change.
	OnChange func(oldFlags, newFlags map[string]Flag)
}

// Manager manages feature flags.
type Manager struct {
	config   Config
	flags    map[string]Flag
	mu       sync.RWMutex
	stopCh   chan struct{}
	stopOnce sync.Once
}

// New creates a new feature flag manager.
func New(cfg Config) *Manager {
	if cfg.RefreshInterval == 0 {
		cfg.RefreshInterval = 1 * time.Minute
	}

	m := &Manager{
		config: cfg,
		flags:  make(map[string]Flag),
		stopCh: make(chan struct{}),
	}

	// Initial load
	if cfg.Source != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		flags, err := cfg.Source.Load(ctx)
		cancel()
		if err == nil {
			m.flags = flags
		}

		// Start watching for changes
		go m.watchChanges()
	}

	return m
}

func (m *Manager) watchChanges() {
	if m.config.Source == nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	changes := m.config.Source.Watch(ctx)
	ticker := time.NewTicker(m.config.RefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case newFlags, ok := <-changes:
			if !ok {
				return
			}
			if newFlags != nil {
				m.updateFlags(newFlags)
			}
		case <-ticker.C:
			// Periodic refresh
			flags, err := m.config.Source.Load(ctx)
			if err == nil && flags != nil {
				m.updateFlags(flags)
			}
		}
	}
}

func (m *Manager) updateFlags(newFlags map[string]Flag) {
	m.mu.Lock()
	oldFlags := m.flags
	m.flags = newFlags
	m.mu.Unlock()

	if m.config.OnChange != nil {
		m.config.OnChange(oldFlags, newFlags)
	}
}

// Stop stops the flag manager.
func (m *Manager) Stop() {
	if m == nil {
		return
	}
	m.stopOnce.Do(func() {
		close(m.stopCh)
	})
}

// IsEnabled checks if a feature flag is enabled for the given context.
func (m *Manager) IsEnabled(ctx context.Context, name string, opts ...Option) bool {
	evalCtx := &EvaluationContext{
		Environment: m.config.DefaultEnvironment,
	}
	for _, opt := range opts {
		opt(evalCtx)
	}

	m.mu.RLock()
	flag, ok := m.flags[name]
	m.mu.RUnlock()

	if !ok {
		return false
	}

	return m.evaluate(flag, evalCtx)
}

// GetFlag returns a flag by name.
func (m *Manager) GetFlag(name string) (Flag, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	flag, ok := m.flags[name]
	return flag, ok
}

// AllFlags returns all flags.
func (m *Manager) AllFlags() map[string]Flag {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]Flag, len(m.flags))
	for k, v := range m.flags {
		result[k] = v
	}
	return result
}

// SetFlag sets a flag (for testing or dynamic updates).
func (m *Manager) SetFlag(flag Flag) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flags[flag.Name] = flag
}

// evaluate evaluates a flag against the given context.
func (m *Manager) evaluate(flag Flag, ctx *EvaluationContext) bool {
	// Check if flag is globally disabled
	if !flag.Enabled {
		return false
	}

	// Check time-based constraints
	now := time.Now()
	if flag.StartTime != nil && now.Before(*flag.StartTime) {
		return false
	}
	if flag.EndTime != nil && now.After(*flag.EndTime) {
		return false
	}

	// Check environment constraints
	if len(flag.Environments) > 0 && ctx.Environment != "" {
		found := false
		for _, env := range flag.Environments {
			if env == ctx.Environment {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Check denied users (takes precedence)
	if ctx.UserID != "" && len(flag.DeniedUsers) > 0 {
		for _, denied := range flag.DeniedUsers {
			if denied == ctx.UserID {
				return false
			}
		}
	}

	// Check allowed users
	if ctx.UserID != "" && len(flag.AllowedUsers) > 0 {
		for _, allowed := range flag.AllowedUsers {
			if allowed == ctx.UserID {
				return true
			}
		}
	}

	// Check allowed orgs
	if ctx.OrgID != "" && len(flag.AllowedOrgs) > 0 {
		for _, allowed := range flag.AllowedOrgs {
			if allowed == ctx.OrgID {
				return true
			}
		}
	}

	// Check rollout percentage
	if flag.RolloutPercentage > 0 && flag.RolloutPercentage < 100 {
		if ctx.UserID == "" {
			return false // Need user ID for percentage rollout
		}
		bucket := hashToBucket(flag.Name, ctx.UserID)
		return bucket < flag.RolloutPercentage
	}

	return flag.Enabled
}

// hashToBucket returns a deterministic bucket (0-99) for a flag and user combination.
func hashToBucket(flagName, userID string) int {
	h := fnv.New32a()
	h.Write([]byte(flagName + ":" + userID))
	return int(h.Sum32() % 100)
}

// EnvSource loads flags from environment variables.
// Flags are expected in the format: FF_FLAG_NAME=true|false|<percentage>
type EnvSource struct {
	prefix string
}

// NewEnvSource creates a new environment variable source.
func NewEnvSource() *EnvSource {
	return &EnvSource{prefix: "FF_"}
}

// NewEnvSourceWithPrefix creates a new environment variable source with custom prefix.
func NewEnvSourceWithPrefix(prefix string) *EnvSource {
	return &EnvSource{prefix: prefix}
}

// Load loads flags from environment variables.
func (s *EnvSource) Load(ctx context.Context) (map[string]Flag, error) {
	flags := make(map[string]Flag)

	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, s.prefix) {
			continue
		}

		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}

		name := strings.ToLower(strings.TrimPrefix(parts[0], s.prefix))
		name = strings.ReplaceAll(name, "_", "-")
		value := parts[1]

		flag := Flag{Name: name}

		// Parse value: "true", "false", or percentage like "50"
		if value == "true" {
			flag.Enabled = true
			flag.RolloutPercentage = 100
		} else if value == "false" {
			flag.Enabled = false
		} else if pct, err := strconv.Atoi(value); err == nil && pct >= 0 && pct <= 100 {
			flag.Enabled = true
			flag.RolloutPercentage = pct
		}

		flags[name] = flag
	}

	return flags, nil
}

// Watch returns nil for env source (no dynamic updates).
func (s *EnvSource) Watch(ctx context.Context) <-chan map[string]Flag {
	return nil
}

// JSONSource loads flags from a JSON string or file.
type JSONSource struct {
	data     string
	filePath string
}

// NewJSONSource creates a source from a JSON string.
func NewJSONSource(jsonData string) *JSONSource {
	return &JSONSource{data: jsonData}
}

// NewJSONFileSource creates a source from a JSON file.
func NewJSONFileSource(filePath string) *JSONSource {
	return &JSONSource{filePath: filePath}
}

// Load loads flags from JSON.
func (s *JSONSource) Load(ctx context.Context) (map[string]Flag, error) {
	var data []byte
	var err error

	if s.filePath != "" {
		data, err = os.ReadFile(s.filePath)
		if err != nil {
			return nil, err
		}
	} else {
		data = []byte(s.data)
	}

	var flags []Flag
	if err := json.Unmarshal(data, &flags); err != nil {
		return nil, err
	}

	result := make(map[string]Flag, len(flags))
	for _, f := range flags {
		result[f.Name] = f
	}

	return result, nil
}

// Watch returns nil for JSON source (no dynamic updates without file watcher).
func (s *JSONSource) Watch(ctx context.Context) <-chan map[string]Flag {
	return nil
}

// MemorySource is an in-memory flag source (useful for testing).
type MemorySource struct {
	mu      sync.RWMutex
	flags   map[string]Flag
	changes chan map[string]Flag
}

// NewMemorySource creates a new in-memory source.
func NewMemorySource() *MemorySource {
	return &MemorySource{
		flags:   make(map[string]Flag),
		changes: make(chan map[string]Flag, 10),
	}
}

// Load returns the current flags.
func (s *MemorySource) Load(ctx context.Context) (map[string]Flag, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]Flag, len(s.flags))
	for k, v := range s.flags {
		result[k] = v
	}
	return result, nil
}

// Watch returns the changes channel.
func (s *MemorySource) Watch(ctx context.Context) <-chan map[string]Flag {
	return s.changes
}

// Set sets a flag and notifies watchers.
func (s *MemorySource) Set(flag Flag) {
	s.mu.Lock()
	s.flags[flag.Name] = flag
	current := make(map[string]Flag, len(s.flags))
	for k, v := range s.flags {
		current[k] = v
	}
	s.mu.Unlock()

	select {
	case s.changes <- current:
	default:
		// concurrency: drop stale notification; periodic refresh reconciles latest snapshot.
	}
}

// Delete removes a flag.
func (s *MemorySource) Delete(name string) {
	s.mu.Lock()
	delete(s.flags, name)
	current := make(map[string]Flag, len(s.flags))
	for k, v := range s.flags {
		current[k] = v
	}
	s.mu.Unlock()

	select {
	case s.changes <- current:
	default:
		// concurrency: drop stale notification; periodic refresh reconciles latest snapshot.
	}
}
