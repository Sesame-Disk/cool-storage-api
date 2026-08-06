// Package downloadadmission contains the process-local admission coordinator
// introduced by subcontract D1. It has no knowledge of HTTP handlers or storage;
// later D phases provide the request identity and hold a Lease around their work.
package downloadadmission

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

// Profile is the fixed D profile enum. Adding a profile is a contract change:
// it changes configuration, metrics and the aggregate-capacity accounting.
type Profile string

const (
	ProfileBlock      Profile = "block"
	ProfileFile       Profile = "file"
	ProfileRaw        Profile = "raw"
	ProfileHistory    Profile = "history"
	ProfileLinkRaw    Profile = "link_raw"
	ProfileZIP        Profile = "zip"
	ProfileLinkInline Profile = "link_inline"
)

// DimensionKind identifies one of the identity dimensions. Node and profile are
// fixed counters rather than entries in the unbounded identity map.
type DimensionKind string

const (
	DimensionAuthUser   DimensionKind = "auth_user"
	DimensionLinkSource DimensionKind = "link_source"
	DimensionClientLink DimensionKind = "client_link"
)

// DimensionKey is deliberately structured. Scope and ID are never joined into
// an untyped string, so equal textual values in different dimensions cannot
// collide.
type DimensionKey struct {
	Kind  DimensionKind
	Scope string
	ID    string
}

// AdmissionRequest is constructed by the identity-specific constructors below.
// Keeping its fields private prevents a future caller from omitting a required
// public-link dimension.
type AdmissionRequest struct {
	profile   Profile
	dimension []DimensionKey
}

// NewAuthenticatedRequest creates the node, optional profile and namespaced
// (organization, user) admission dimensions.
func NewAuthenticatedRequest(profile Profile, orgID, userID string) (AdmissionRequest, error) {
	if !validProfile(profile) {
		return AdmissionRequest{}, fmt.Errorf("invalid download admission profile %q", profile)
	}
	orgID = strings.TrimSpace(orgID)
	userID = strings.TrimSpace(userID)
	if orgID == "" {
		return AdmissionRequest{}, fmt.Errorf("organization id is required")
	}
	if userID == "" {
		return AdmissionRequest{}, fmt.Errorf("user id is required")
	}
	return AdmissionRequest{
		profile:   profile,
		dimension: []DimensionKey{{Kind: DimensionAuthUser, Scope: orgID, ID: userID}},
	}, nil
}

// NewPublicLinkRequest creates the stable-source and normalized-client
// dimensions. The caller must obtain normalizedClientIP through the trusted
// proxy-aware HTTP resolver; this package deliberately does not parse headers.
func NewPublicLinkRequest(profile Profile, sourceID, normalizedClientIP string) (AdmissionRequest, error) {
	if !validProfile(profile) {
		return AdmissionRequest{}, fmt.Errorf("invalid download admission profile %q", profile)
	}
	sourceID = strings.TrimSpace(sourceID)
	normalizedClientIP = strings.TrimSpace(normalizedClientIP)
	if sourceID == "" {
		return AdmissionRequest{}, fmt.Errorf("source id is required")
	}
	if normalizedClientIP == "" {
		return AdmissionRequest{}, fmt.Errorf("normalized client ip is required")
	}
	return AdmissionRequest{
		profile: profile,
		dimension: []DimensionKey{
			{Kind: DimensionLinkSource, Scope: "public-link", ID: sourceID},
			{Kind: DimensionClientLink, Scope: normalizedClientIP, ID: sourceID},
		},
	}, nil
}

// RejectReason is a fixed explanation for why admission did not succeed.
// Capacity reasons are metric-safe and intentionally exclude every identity.
type RejectReason string

const (
	RejectNodeFull            RejectReason = "node_full"
	RejectProfileFull         RejectReason = "profile_full"
	RejectAuthUserFull        RejectReason = "auth_user_full"
	RejectLinkSourceFull      RejectReason = "link_source_full"
	RejectClientLinkFull      RejectReason = "client_link_full"
	RejectNodeQueueFull       RejectReason = "node_queue_full"
	RejectAuthUserQueueFull   RejectReason = "auth_user_queue_full"
	RejectLinkSourceQueueFull RejectReason = "link_source_queue_full"
	RejectClientLinkQueueFull RejectReason = "client_link_queue_full"
	RejectAdmissionTimeout    RejectReason = "admission_timeout"
	RejectClientGone          RejectReason = "client_gone"
	// RejectInvalidRequest is returned for an impossible package-level request.
	// It is not a capacity metric label because constructors reject these inputs.
	RejectInvalidRequest RejectReason = "invalid_request"
)

// ReleaseCause is a fixed metric-safe explanation for why an admitted lease
// disappeared. D3-D6 use the non-completed causes as their failure evidence.
type ReleaseCause string

const (
	ReleaseCompleted          ReleaseCause = "completed"
	ReleaseClientDisconnect   ReleaseCause = "client_disconnect"
	ReleasePreparationTimeout ReleaseCause = "preparation_timeout"
	ReleaseIdleWriteTimeout   ReleaseCause = "idle_write_timeout"
	ReleaseStorageError       ReleaseCause = "storage_error"
	ReleaseResponseError      ReleaseCause = "response_error"
	ReleasePanic              ReleaseCause = "panic"
)

// DeadlinePhase identifies the two post-admission deadline families.
type DeadlinePhase string

const (
	DeadlinePreparation DeadlinePhase = "preparation"
	DeadlineIdleWrite   DeadlinePhase = "idle_write"
)

type gateKind string

const (
	gateNode       gateKind = "node"
	gateProfile    gateKind = "profile"
	gateAuthUser   gateKind = "auth_user"
	gateLinkSource gateKind = "link_source"
	gateClientLink gateKind = "client_link"
)

var (
	activeByProfileGauge = map[Profile]prometheus.Gauge{
		ProfileBlock:      metrics.DownloadAdmissionActiveByProfile.WithLabelValues(string(ProfileBlock)),
		ProfileFile:       metrics.DownloadAdmissionActiveByProfile.WithLabelValues(string(ProfileFile)),
		ProfileRaw:        metrics.DownloadAdmissionActiveByProfile.WithLabelValues(string(ProfileRaw)),
		ProfileHistory:    metrics.DownloadAdmissionActiveByProfile.WithLabelValues(string(ProfileHistory)),
		ProfileLinkRaw:    metrics.DownloadAdmissionActiveByProfile.WithLabelValues(string(ProfileLinkRaw)),
		ProfileZIP:        metrics.DownloadAdmissionActiveByProfile.WithLabelValues(string(ProfileZIP)),
		ProfileLinkInline: metrics.DownloadAdmissionActiveByProfile.WithLabelValues(string(ProfileLinkInline)),
	}
	waitersByGateGauge = map[gateKind]prometheus.Gauge{
		gateNode:       metrics.DownloadAdmissionWaitersByGate.WithLabelValues(string(gateNode)),
		gateProfile:    metrics.DownloadAdmissionWaitersByGate.WithLabelValues(string(gateProfile)),
		gateAuthUser:   metrics.DownloadAdmissionWaitersByGate.WithLabelValues(string(gateAuthUser)),
		gateLinkSource: metrics.DownloadAdmissionWaitersByGate.WithLabelValues(string(gateLinkSource)),
		gateClientLink: metrics.DownloadAdmissionWaitersByGate.WithLabelValues(string(gateClientLink)),
	}
	trackedIdentitiesGauge = map[DimensionKind]prometheus.Gauge{
		DimensionAuthUser:   metrics.DownloadAdmissionTrackedIdentities.WithLabelValues(string(DimensionAuthUser)),
		DimensionLinkSource: metrics.DownloadAdmissionTrackedIdentities.WithLabelValues(string(DimensionLinkSource)),
		DimensionClientLink: metrics.DownloadAdmissionTrackedIdentities.WithLabelValues(string(DimensionClientLink)),
	}
	identityOccupancy = map[DimensionKind]prometheus.Observer{
		DimensionAuthUser:   metrics.DownloadAdmissionOccupancy.WithLabelValues(string(DimensionAuthUser)),
		DimensionLinkSource: metrics.DownloadAdmissionOccupancy.WithLabelValues(string(DimensionLinkSource)),
		DimensionClientLink: metrics.DownloadAdmissionOccupancy.WithLabelValues(string(DimensionClientLink)),
	}
)

type identityState struct {
	active  int
	waiters int
}

type waiter struct {
	request AdmissionRequest
	blocked []gateKind
}

// Coordinator grants all applicable dimensions under one mutex. Waiters use a
// close-and-replace notification channel: a release wakes all bounded waiters,
// which re-check their complete gate set and therefore cannot create unrelated
// identity head-of-line blocking.
//
// D4 must construct exactly one enabled Coordinator per process and share that
// pointer with every producer. New deliberately does not enforce a global
// singleton, so tests can build isolated instances; two enabled coordinators in
// one process would multiply the node cap and make the shared gauges meaningless.
type Coordinator struct {
	cfg config.DownloadAdmissionConfig

	mu                sync.Mutex
	active            int
	activeByProfile   map[Profile]int
	identities        map[DimensionKey]*identityState
	trackedIdentities map[DimensionKind]int
	waiters           map[*waiter]struct{}
	waitersByGate     map[gateKind]int
	notify            chan struct{}
}

// New creates a coordinator. Disabled configurations still return a usable
// no-op coordinator so later callers do not need nil branching. The standalone
// D validation is shared with internal/config so package users cannot bypass
// the D bounds.
func New(cfg *config.DownloadAdmissionConfig) (*Coordinator, error) {
	if cfg == nil {
		return nil, fmt.Errorf("download admission config is required")
	}
	if err := config.ValidateDownloadAdmissionConfig(*cfg); err != nil {
		return nil, err
	}
	publishCapacityMetrics(*cfg)
	return &Coordinator{
		cfg:               *cfg,
		activeByProfile:   make(map[Profile]int),
		identities:        make(map[DimensionKey]*identityState),
		trackedIdentities: make(map[DimensionKind]int),
		waiters:           make(map[*waiter]struct{}),
		waitersByGate:     make(map[gateKind]int),
		notify:            make(chan struct{}),
	}, nil
}

// Acquire either returns one lease holding every applicable dimension or a
// fixed rejection reason. A request never holds partial capacity while waiting.
func (c *Coordinator) Acquire(ctx context.Context, request AdmissionRequest) (*Lease, RejectReason) {
	if c == nil {
		return nil, RejectClientGone
	}
	if !c.cfg.Enabled {
		return &Lease{coordinator: c, request: request}, ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateRequest(request); err != nil {
		return nil, RejectInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		c.observeRejected(request, RejectClientGone)
		return nil, RejectClientGone
	}
	started := time.Now()
	c.mu.Lock()
	// Re-check under the lock. The check above happened before the mutex wait,
	// and that wait is unbounded under contention, so a client can disconnect
	// while this request is queued for the lock. Granting then would hand a slot
	// to a request that is already gone and record it as an admission. Parked
	// waiters already revalidate this way in waitForLease; the invariant is that
	// no grant is issued for a context observed cancelled at decision time, and
	// it has to hold on both paths.
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		c.observeWait(started, "cancelled")
		c.observeRejected(request, RejectClientGone)
		return nil, RejectClientGone
	}
	if granted, blocked := c.tryGrantLocked(request); granted {
		lease := c.grantLocked(request)
		c.mu.Unlock()
		c.observeWait(started, "admitted")
		return lease, ""
	} else {
		reason := capacityReason(blocked)
		if c.cfg.AdmissionWait <= 0 {
			c.mu.Unlock()
			c.observeWait(started, "refused")
			c.observeRejected(request, reason)
			return nil, reason
		}
		if queueReason := c.queueFullReasonLocked(request); queueReason != "" {
			c.mu.Unlock()
			c.observeWait(started, "refused")
			c.observeRejected(request, queueReason)
			return nil, queueReason
		}
		w := &waiter{request: request, blocked: blocked}
		c.addWaiterLocked(w)
		notify := c.notify
		c.mu.Unlock()

		return c.waitForLease(ctx, request, w, notify, started)
	}
}

// RetryAfterSeconds serializes the independent retry_after setting with the
// HTTP Retry-After second granularity. It remains useful while admission_wait is
// zero because long-lived transfer slots do not free on the queue timescale.
func (c *Coordinator) RetryAfterSeconds() int {
	if c == nil || c.cfg.RetryAfter <= 0 {
		return 1
	}
	seconds := int(math.Ceil(c.cfg.RetryAfter.Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}

// Lease represents one all-or-none admission. Release is safe to call from
// every handler exit path, including a deferred panic cleanup.
type Lease struct {
	coordinator *Coordinator
	request     AdmissionRequest
	once        sync.Once
	mu          sync.Mutex
	deadlines   map[DeadlinePhase]bool
	writerOnce  sync.Once
}

// Release returns every dimension held by this lease exactly once.
func (l *Lease) Release(cause ReleaseCause) {
	if l == nil || l.coordinator == nil {
		return
	}
	l.once.Do(func() {
		if !l.coordinator.cfg.Enabled {
			return
		}
		l.coordinator.release(l.request, cause)
	})
}

// RecordDeadlineExpired records one deadline per phase. It is intentionally a
// lease method so D3 can attribute the phase before releasing the lease.
func (l *Lease) RecordDeadlineExpired(phase DeadlinePhase) {
	if l == nil || l.coordinator == nil || !l.coordinator.cfg.Enabled || !validDeadlinePhase(phase) {
		return
	}
	l.mu.Lock()
	if l.deadlines == nil {
		l.deadlines = make(map[DeadlinePhase]bool)
	}
	if l.deadlines[phase] {
		l.mu.Unlock()
		return
	}
	l.deadlines[phase] = true
	l.mu.Unlock()
	metrics.DownloadAdmissionDeadlineExpiredTotal.WithLabelValues(string(phase)).Inc()
}

// RecordWriterUnreachable records that the connection deadline could not be
// installed. D3 uses this as a fail-closed wiring signal.
func (l *Lease) RecordWriterUnreachable() {
	if l == nil || l.coordinator == nil || !l.coordinator.cfg.Enabled {
		return
	}
	l.writerOnce.Do(func() {
		metrics.DownloadAdmissionWriterUnreachableTotal.Inc()
	})
}

func (c *Coordinator) waitForLease(ctx context.Context, request AdmissionRequest, w *waiter, notify chan struct{}, started time.Time) (*Lease, RejectReason) {
	waitCtx, cancel := context.WithTimeout(ctx, c.cfg.AdmissionWait)
	defer cancel()
	for {
		select {
		case <-waitCtx.Done():
			c.mu.Lock()
			c.removeWaiterLocked(w)
			c.mu.Unlock()
			if ctx.Err() != nil {
				c.observeWait(started, "cancelled")
				c.observeRejected(request, RejectClientGone)
				return nil, RejectClientGone
			}
			c.observeWait(started, "timeout")
			c.observeRejected(request, RejectAdmissionTimeout)
			return nil, RejectAdmissionTimeout
		case <-notify:
			c.mu.Lock()
			if ctx.Err() != nil {
				c.removeWaiterLocked(w)
				c.mu.Unlock()
				c.observeWait(started, "cancelled")
				c.observeRejected(request, RejectClientGone)
				return nil, RejectClientGone
			}
			if waitCtx.Err() != nil {
				c.removeWaiterLocked(w)
				c.mu.Unlock()
				c.observeWait(started, "timeout")
				c.observeRejected(request, RejectAdmissionTimeout)
				return nil, RejectAdmissionTimeout
			}
			if granted, blocked := c.tryGrantLocked(request); granted {
				lease := c.grantLocked(request)
				c.removeWaiterLocked(w)
				c.mu.Unlock()
				c.observeWait(started, "admitted")
				return lease, ""
			} else {
				c.updateWaiterBlockedLocked(w, blocked)
				notify = c.notify
				c.mu.Unlock()
			}
		}
	}
}

func (c *Coordinator) tryGrantLocked(request AdmissionRequest) (bool, []gateKind) {
	blocked := make([]gateKind, 0, 5)
	if c.active >= c.cfg.MaxActivePerNode {
		blocked = append(blocked, gateNode)
	}
	if cap := profileCap(c.cfg, request.profile); cap > 0 && c.activeByProfile[request.profile] >= cap {
		blocked = append(blocked, gateProfile)
	}
	for _, dimension := range request.dimension {
		cap := dimensionCap(c.cfg, dimension.Kind)
		state := c.identities[dimension]
		if cap > 0 && state != nil && state.active >= cap {
			blocked = append(blocked, gateForDimension(dimension.Kind))
		}
	}
	return len(blocked) == 0, blocked
}

func (c *Coordinator) grantLocked(request AdmissionRequest) *Lease {
	c.active++
	c.activeByProfile[request.profile]++
	for _, dimension := range request.dimension {
		state := c.identities[dimension]
		if state == nil {
			state = &identityState{}
			c.identities[dimension] = state
			c.trackIdentityLocked(dimension.Kind)
		}
		state.active++
		identityOccupancy[dimension.Kind].Observe(float64(state.active))
	}
	activeByProfileGauge[request.profile].Set(float64(c.activeByProfile[request.profile]))
	setAdmissionGauges(c)
	return &Lease{coordinator: c, request: request}
}

func (c *Coordinator) release(request AdmissionRequest, cause ReleaseCause) {
	if !validReleaseCause(cause) {
		cause = ReleaseResponseError
	}
	c.mu.Lock()
	c.active--
	c.activeByProfile[request.profile]--
	for _, dimension := range request.dimension {
		state := c.identities[dimension]
		if state == nil || state.active <= 0 {
			continue
		}
		state.active--
		if state.active == 0 && state.waiters == 0 {
			delete(c.identities, dimension)
			c.untrackIdentityLocked(dimension.Kind)
		}
	}
	activeByProfileGauge[request.profile].Set(float64(c.activeByProfile[request.profile]))
	setAdmissionGauges(c)
	c.signalLocked()
	c.mu.Unlock()
	metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(cause)).Inc()
}

func (c *Coordinator) addWaiterLocked(w *waiter) {
	c.waiters[w] = struct{}{}
	for _, dimension := range w.request.dimension {
		state := c.identities[dimension]
		if state == nil {
			state = &identityState{}
			c.identities[dimension] = state
			c.trackIdentityLocked(dimension.Kind)
		}
		state.waiters++
	}
	for _, gate := range w.blocked {
		c.waitersByGate[gate]++
		waitersByGateGauge[gate].Set(float64(c.waitersByGate[gate]))
	}
	setAdmissionGauges(c)
}

func (c *Coordinator) removeWaiterLocked(w *waiter) {
	if _, ok := c.waiters[w]; !ok {
		return
	}
	delete(c.waiters, w)
	for _, dimension := range w.request.dimension {
		state := c.identities[dimension]
		if state == nil || state.waiters <= 0 {
			continue
		}
		state.waiters--
		if state.active == 0 && state.waiters == 0 {
			delete(c.identities, dimension)
			c.untrackIdentityLocked(dimension.Kind)
		}
	}
	for _, gate := range w.blocked {
		c.waitersByGate[gate]--
		waitersByGateGauge[gate].Set(float64(c.waitersByGate[gate]))
	}
	w.blocked = nil
	setAdmissionGauges(c)
}

func (c *Coordinator) updateWaiterBlockedLocked(w *waiter, blocked []gateKind) {
	for _, gate := range w.blocked {
		c.waitersByGate[gate]--
		waitersByGateGauge[gate].Set(float64(c.waitersByGate[gate]))
	}
	w.blocked = blocked
	for _, gate := range w.blocked {
		c.waitersByGate[gate]++
		waitersByGateGauge[gate].Set(float64(c.waitersByGate[gate]))
	}
}

func (c *Coordinator) queueFullReasonLocked(request AdmissionRequest) RejectReason {
	if c.cfg.MaxWaitersPerNode <= 0 || len(c.waiters) >= c.cfg.MaxWaitersPerNode {
		return RejectNodeQueueFull
	}
	for _, dimension := range request.dimension {
		state := c.identities[dimension]
		waiters := 0
		if state != nil {
			waiters = state.waiters
		}
		if c.cfg.MaxWaitersPerIdentity <= 0 || waiters >= c.cfg.MaxWaitersPerIdentity {
			switch dimension.Kind {
			case DimensionAuthUser:
				return RejectAuthUserQueueFull
			case DimensionLinkSource:
				return RejectLinkSourceQueueFull
			case DimensionClientLink:
				return RejectClientLinkQueueFull
			}
		}
	}
	return ""
}

func (c *Coordinator) signalLocked() {
	if len(c.waiters) == 0 {
		return
	}
	close(c.notify)
	c.notify = make(chan struct{})
}

func (c *Coordinator) observeRejected(request AdmissionRequest, reason RejectReason) {
	metrics.DownloadAdmissionRejectedTotal.WithLabelValues(string(reason)).Inc()
	if validProfile(request.profile) {
		metrics.DownloadAdmissionRejectedByProfileTotal.WithLabelValues(string(request.profile), string(reason)).Inc()
	}
}

func (c *Coordinator) observeWait(started time.Time, outcome string) {
	metrics.DownloadAdmissionWaitSeconds.WithLabelValues(outcome).Observe(time.Since(started).Seconds())
}

func setAdmissionGauges(c *Coordinator) {
	metrics.DownloadAdmissionActiveCurrent.Set(float64(c.active))
	metrics.DownloadAdmissionEntriesCurrent.Set(float64(c.active + len(c.waiters)))
	metrics.DownloadAdmissionWaitersCurrent.Set(float64(len(c.waiters)))
}

func (c *Coordinator) trackIdentityLocked(kind DimensionKind) {
	c.trackedIdentities[kind]++
	trackedIdentitiesGauge[kind].Set(float64(c.trackedIdentities[kind]))
}

func (c *Coordinator) untrackIdentityLocked(kind DimensionKind) {
	c.trackedIdentities[kind]--
	trackedIdentitiesGauge[kind].Set(float64(c.trackedIdentities[kind]))
}

func capacityReason(blocked []gateKind) RejectReason {
	if len(blocked) == 0 {
		return RejectNodeFull
	}
	switch blocked[0] {
	case gateNode:
		return RejectNodeFull
	case gateProfile:
		return RejectProfileFull
	case gateAuthUser:
		return RejectAuthUserFull
	case gateLinkSource:
		return RejectLinkSourceFull
	case gateClientLink:
		return RejectClientLinkFull
	default:
		return RejectNodeFull
	}
}

func gateForDimension(kind DimensionKind) gateKind {
	switch kind {
	case DimensionAuthUser:
		return gateAuthUser
	case DimensionLinkSource:
		return gateLinkSource
	case DimensionClientLink:
		return gateClientLink
	default:
		return gateNode
	}
}

func dimensionCap(cfg config.DownloadAdmissionConfig, kind DimensionKind) int {
	switch kind {
	case DimensionAuthUser:
		return cfg.MaxActivePerAuthUser
	case DimensionLinkSource:
		return cfg.MaxActivePerLinkSource
	case DimensionClientLink:
		return cfg.MaxActivePerClientLink
	default:
		return 0
	}
}

func profileCap(cfg config.DownloadAdmissionConfig, profile Profile) int {
	switch profile {
	case ProfileBlock:
		return cfg.MaxActiveBlock
	case ProfileFile:
		return cfg.MaxActiveFile
	case ProfileRaw:
		return cfg.MaxActiveRaw
	case ProfileHistory:
		return cfg.MaxActiveHistory
	case ProfileLinkRaw:
		return cfg.MaxActiveLinkRaw
	case ProfileZIP:
		return cfg.MaxActiveZIP
	case ProfileLinkInline:
		return cfg.MaxActiveLinkInline
	default:
		return 0
	}
}

func allProfiles() []Profile {
	return []Profile{ProfileBlock, ProfileFile, ProfileRaw, ProfileHistory, ProfileLinkRaw, ProfileZIP, ProfileLinkInline}
}

func validProfile(profile Profile) bool {
	switch profile {
	case ProfileBlock, ProfileFile, ProfileRaw, ProfileHistory, ProfileLinkRaw, ProfileZIP, ProfileLinkInline:
		return true
	default:
		return false
	}
}

func validDeadlinePhase(phase DeadlinePhase) bool {
	return phase == DeadlinePreparation || phase == DeadlineIdleWrite
}

func validReleaseCause(cause ReleaseCause) bool {
	switch cause {
	case ReleaseCompleted, ReleaseClientDisconnect, ReleasePreparationTimeout, ReleaseIdleWriteTimeout, ReleaseStorageError, ReleaseResponseError, ReleasePanic:
		return true
	default:
		return false
	}
}

func validateRequest(request AdmissionRequest) error {
	if !validProfile(request.profile) || len(request.dimension) == 0 {
		return fmt.Errorf("invalid admission request")
	}
	if len(request.dimension) > 2 {
		return fmt.Errorf("admission request has too many identity dimensions")
	}
	seen := make(map[DimensionKind]bool, len(request.dimension))
	for _, dimension := range request.dimension {
		if dimension.Kind != DimensionAuthUser && dimension.Kind != DimensionLinkSource && dimension.Kind != DimensionClientLink {
			return fmt.Errorf("invalid admission dimension %q", dimension.Kind)
		}
		if seen[dimension.Kind] || strings.TrimSpace(dimension.Scope) == "" || strings.TrimSpace(dimension.ID) == "" {
			return fmt.Errorf("invalid admission dimension")
		}
		seen[dimension.Kind] = true
	}
	if len(request.dimension) == 1 && !seen[DimensionAuthUser] {
		return fmt.Errorf("authenticated admission request requires auth_user dimension")
	}
	if len(request.dimension) == 2 && (!seen[DimensionLinkSource] || !seen[DimensionClientLink]) {
		return fmt.Errorf("public-link admission request requires link_source and client_link dimensions")
	}
	return nil
}

// publishCapacityMetrics exports the capacities this coordinator was built
// with. In auto mode they are derived at startup from the detected memory
// limit, so a deployment's real ceiling is not in its config file and an
// operator — or a drill that has to saturate the node — otherwise has no way to
// read it except by filling the node and watching the plateau.
func publishCapacityMetrics(cfg config.DownloadAdmissionConfig) {
	if !cfg.Enabled {
		// Publish zeros rather than nothing: an absent series is
		// indistinguishable from a scrape that arrived before startup, while a
		// zero says plainly that nothing is bounded.
		for _, setting := range capacityMetricSettings {
			metrics.DownloadAdmissionCapacity.WithLabelValues(setting).Set(0)
		}
		metrics.DownloadAdmissionMemoryBudgetBytes.Set(0)
		metrics.DownloadAdmissionMemoryBudgetEffectiveBytes.Set(0)
		return
	}
	for setting, value := range publishedCapacitySettings(cfg) {
		metrics.DownloadAdmissionCapacity.WithLabelValues(setting).Set(float64(value))
	}
	metrics.DownloadAdmissionMemoryBudgetBytes.Set(float64(cfg.MemoryBudgetBytes))
	margin := cfg.SafetyMarginPercent
	if margin < 0 || margin >= 100 {
		margin = 0
	}
	metrics.DownloadAdmissionMemoryBudgetEffectiveBytes.Set(float64(cfg.MemoryBudgetBytes / 100 * int64(100-margin)))
}

// publishedCapacitySettings is the single description of which capacities are
// exported, so the live and zeroing paths cannot drift apart.
func publishedCapacitySettings(cfg config.DownloadAdmissionConfig) map[string]int {
	return map[string]int{
		"max_active_per_node":        cfg.MaxActivePerNode,
		"max_active_per_auth_user":   cfg.MaxActivePerAuthUser,
		"max_active_per_link_source": cfg.MaxActivePerLinkSource,
		"max_active_per_client_link": cfg.MaxActivePerClientLink,
		"max_waiters_per_identity":   cfg.MaxWaitersPerIdentity,
		"max_waiters_per_node":       cfg.MaxWaitersPerNode,
		"max_active_block":           cfg.MaxActiveBlock,
		"max_active_file":            cfg.MaxActiveFile,
		"max_active_raw":             cfg.MaxActiveRaw,
		"max_active_history":         cfg.MaxActiveHistory,
		"max_active_link_raw":        cfg.MaxActiveLinkRaw,
		"max_active_zip":             cfg.MaxActiveZIP,
		"max_active_link_inline":     cfg.MaxActiveLinkInline,
	}
}

var capacityMetricSettings = []string{
	"max_active_per_node",
	"max_active_per_auth_user",
	"max_active_per_link_source",
	"max_active_per_client_link",
	"max_waiters_per_identity",
	"max_waiters_per_node",
	"max_active_block",
	"max_active_file",
	"max_active_raw",
	"max_active_history",
	"max_active_link_raw",
	"max_active_zip",
	"max_active_link_inline",
}
