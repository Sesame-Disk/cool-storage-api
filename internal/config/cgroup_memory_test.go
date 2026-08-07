package config

import (
	"os"
	"strings"
	"testing"
)

// TestMain pins the detected container limit for the whole package.
//
// Auto capacity derives from the host, so any test that reaches Validate()
// with the section enabled is otherwise a function of the runner's own
// cgroup: green on a laptop, and on a sufficiently small agent failing
// outright because the budget cannot fit one raw slot plus one stream slot.
// Pinning here rather than per test means a test added later cannot
// reintroduce the dependency by forgetting to opt out; the real detection is
// covered by TestParseCgroupMemoryLimit and by the integration probe against
// a container that actually has a limit.
func TestMain(m *testing.M) {
	previous := cgroupMemoryLimit
	cgroupMemoryLimit = func() (int64, bool) { return 0, false }
	code := m.Run()
	cgroupMemoryLimit = previous
	os.Exit(code)
}

// withCgroupMemoryLimit pins the detected limit for one test. Anything that
// exercises auto capacity is otherwise a function of the runner's own cgroup.
func withCgroupMemoryLimit(t *testing.T, limit int64, ok bool) {
	t.Helper()
	previous := cgroupMemoryLimit
	cgroupMemoryLimit = func() (int64, bool) { return limit, ok }
	t.Cleanup(func() { cgroupMemoryLimit = previous })
}

func TestAutoCapacityUsesTheDetectedCgroupLimit(t *testing.T) {
	withCgroupMemoryLimit(t, 8*1024*1024*1024, true)

	cfg := DefaultConfig()
	cfg.Auth.DevMode = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("auto capacity under an 8 GiB limit: %v", err)
	}
	// 25% of 8 GiB is the same 2 GiB the fallback uses, so the shipped baseline
	// is what a detected limit of that size derives too.
	if got := cfg.DownloadAdmission.MemoryBudgetBytes; got != 2*1024*1024*1024 {
		t.Fatalf("derived budget = %d, want 25%% of the 8 GiB limit", got)
	}
	if cfg.DownloadAdmission.MaxActivePerNode != 16 || cfg.DownloadAdmission.MaxActiveRaw != 4 {
		t.Fatalf("derived capacities = node %d/raw %d, want 16/4",
			cfg.DownloadAdmission.MaxActivePerNode, cfg.DownloadAdmission.MaxActiveRaw)
	}
}

// TestAutoCapacityScalesWithTheDetectedLimit is the property the whole feature
// exists for, and the reason the shipped YAML numbers are a reference rather
// than the truth: a bigger container derives a bigger ceiling.
func TestAutoCapacityScalesWithTheDetectedLimit(t *testing.T) {
	var previous int
	for _, limit := range []int64{4, 8, 16} {
		withCgroupMemoryLimit(t, limit*1024*1024*1024, true)
		cfg := DefaultConfig()
		cfg.Auth.DevMode = true
		if err := cfg.Validate(); err != nil {
			t.Fatalf("%d GiB limit: %v", limit, err)
		}
		got := cfg.DownloadAdmission.MaxActivePerNode
		if got <= previous {
			t.Fatalf("%d GiB limit derived node cap %d, not more than the smaller container's %d", limit, got, previous)
		}
		previous = got
	}
}

func TestParseCgroupMemoryLimit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  int64
		ok    bool
	}{
		{name: "v2 limit", value: "8589934592\n", want: 8 * 1024 * 1024 * 1024, ok: true},
		{name: "unlimited", value: "max", ok: false},
		{name: "empty", value: "", ok: false},
		{name: "invalid", value: "not-a-number", ok: false},
		{name: "sentinel unlimited", value: "1152921504606846976", ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseCgroupMemoryLimit(tc.value)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("parseCgroupMemoryLimit(%q) = (%d, %t), want (%d, %t)", tc.value, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestValidateDownloadAdmissionCgroupBudget(t *testing.T) {
	d := defaultDownloadAdmissionConfig()
	d.MemoryBudgetBytes = 2 * 1024 * 1024 * 1024

	if err := validateDownloadAdmissionCgroupBudgetForLimit(d, 8*1024*1024*1024); err != nil {
		t.Fatalf("2 GiB budget under 8 GiB limit: %v", err)
	}
	d.MemoryBudgetBytes = 2 * 1024 * 1024 * 1024
	if err := validateDownloadAdmissionCgroupBudgetForLimit(d, 4*1024*1024*1024); err == nil {
		t.Fatal("2 GiB budget under 4 GiB limit was accepted; want 25% cgroup guard")
	}
	d.MemoryBudgetBytes = 1 * 1024 * 1024 * 1024
	if err := validateDownloadAdmissionCgroupBudgetForLimit(d, 4*1024*1024*1024); err != nil {
		t.Fatalf("1 GiB budget under 4 GiB limit: %v", err)
	}
}

// TestBudgetSourceIsAlwaysAttributed pins the contract the provenance metric
// rests on: exactly one source is in effect, in every mode. Manual returned
// before recording anything, so a perfectly valid explicit budget published all
// three sources as zero and an operator reading the metric could not tell why.
func TestBudgetSourceIsAlwaysAttributed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		limit   int64
		haveCap bool
		modify  func(*Config)
		want    string
	}{
		{name: "auto from cgroup", limit: 8 << 30, haveCap: true, want: "cgroup"},
		{name: "auto without a limit", want: "fallback"},
		{
			name:   "auto with an explicit budget",
			modify: func(cfg *Config) { cfg.DownloadAdmission.MemoryBudgetBytes = 2 << 30 },
			want:   "configured",
		},
		{
			name: "manual",
			modify: func(cfg *Config) {
				cfg.DownloadAdmission.CapacityMode = "manual"
				cfg.DownloadAdmission.MemoryBudgetBytes = 2 << 30
			},
			want: "configured",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withCgroupMemoryLimit(t, tc.limit, tc.haveCap)
			cfg := DefaultConfig()
			cfg.Auth.DevMode = true
			if tc.modify != nil {
				tc.modify(cfg)
			}

			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate() = %v", err)
			}
			if got := cfg.DownloadBudgetSource(); got != tc.want {
				t.Fatalf("budget source = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestValidateReadsTheContainerLimitOnce pins the snapshot. The derivation, the
// floor advice and the cgroup budget guard all need the container limit, and the
// value can be rewritten while a process starts — so a pass that sampled it per
// consumer could derive capacities for one container size and then admit or
// refuse them against another. Neither answer is wrong alone; the pair is what
// has no meaning.
func TestValidateReadsTheContainerLimitOnce(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"auto derives the budget", func(*Config) {}},
		{"auto with an explicit budget", func(c *Config) {
			c.DownloadAdmission.MemoryBudgetBytes = 1 << 30
		}},
		// Manual keeps the shipped caps rather than deriving smaller ones, so its
		// budget has to be the one those caps were measured against.
		{"manual", func(c *Config) {
			c.DownloadAdmission.CapacityMode = "manual"
			c.DownloadAdmission.MemoryBudgetBytes = DefaultDownloadAdmissionMemoryBudgetBytes
		}},
		{"disabled", func(c *Config) { c.DownloadAdmission.Enabled = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			previous := cgroupMemoryLimit
			cgroupMemoryLimit = func() (int64, bool) {
				reads++
				return 8 << 30, true
			}
			t.Cleanup(func() { cgroupMemoryLimit = previous })

			cfg := DefaultConfig()
			cfg.Auth.DevMode = true
			tc.mutate(cfg)

			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate() = %v", err)
			}
			if reads != 1 {
				t.Fatalf("read the container limit %d times; one validation must observe one machine", reads)
			}
		})
	}
}

// TestCgroupBudgetGuardStillAppliesInManualMode guards the way that snapshot can
// silently switch the guard off. The limit has to be read above the manual and
// disabled early returns, not beside the auto derivation that consumes it:
// moving it down would hand this path a zero, and a zero reads as "no container
// limit", which is how the guard declines. Manual mode is exactly where an
// operator writes their own budget, so it is the path that most needs charging
// against the container.
func TestCgroupBudgetGuardStillAppliesInManualMode(t *testing.T) {
	withCgroupMemoryLimit(t, 4<<30, true) // the default 25% share is 1 GiB
	cfg := DefaultConfig()
	cfg.Auth.DevMode = true
	cfg.DownloadAdmission.CapacityMode = "manual"
	cfg.DownloadAdmission.MemoryBudgetBytes = 2 << 30 // half the container

	err := cfg.Validate()
	if err == nil {
		t.Fatal("a manual budget at 50% of the container validated against a 25% share")
	}
	if !strings.Contains(err.Error(), "of the detected cgroup memory limit") {
		t.Fatalf("refused for some other reason, so this does not prove the guard ran: %v", err)
	}
}

// TestBudgetSourceSurvivesRevalidation covers the metric's whole reason for
// existing. Auto mode materialises the derived budget into the config, so a
// second Validate() sees a positive number and would call it "configured" —
// relabelling a derived budget as an explicit one is exactly the confusion this
// metric was added to prevent.
func TestBudgetSourceSurvivesRevalidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		limit   int64
		haveCap bool
		want    string
	}{
		{name: "derived from cgroup", limit: 8 << 30, haveCap: true, want: "cgroup"},
		{name: "reference fallback", want: "fallback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withCgroupMemoryLimit(t, tc.limit, tc.haveCap)
			cfg := DefaultConfig()
			cfg.Auth.DevMode = true

			if err := cfg.Validate(); err != nil {
				t.Fatalf("first Validate() = %v", err)
			}
			if got := cfg.DownloadBudgetSource(); got != tc.want {
				t.Fatalf("budget source = %q, want %q", got, tc.want)
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("second Validate() = %v", err)
			}
			if got := cfg.DownloadBudgetSource(); got != tc.want {
				t.Fatalf("budget source after revalidation = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSmallContainerFailureNamesTheLevers pins the message an operator meets
// when a container is too small for the shipped design. It is the one refusal
// reachable without misconfiguring anything, so "too small" on its own leaves
// them stuck — and advice that names a setting which cannot resolve the case
// is worse, because following it produces a second refusal.
func TestSmallContainerFailureNamesTheLevers(t *testing.T) {
	t.Run("derived from a small container", func(t *testing.T) {
		withCgroupMemoryLimit(t, 1<<30, true) // 1 GiB derives a 256 MiB budget
		cfg := DefaultConfig()
		cfg.Auth.DevMode = true

		err := cfg.Validate()
		if err == nil {
			t.Fatal("a 1 GiB container validated; the derivation cannot fit one raw slot plus one stream slot")
		}
		// The percentage is the lever that works here; an explicit budget is
		// held to the same share, so it must be described as bounded rather than
		// offered as an alternative.
		for _, want := range []string{
			"memory_budget_percent",
			"held to that same share",
			"fileview.max_iwork_source_bytes",
			"enabled: false",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("failure does not mention %q: %v", want, err)
			}
		}
		// max_iwork_preview_bytes does not bind at the shipped values, so naming
		// it would send the operator to a setting that changes nothing.
		if strings.Contains(err.Error(), "max_iwork_preview_bytes") {
			t.Errorf("failure names a lever that does not move this floor: %v", err)
		}
	})

	t.Run("configured explicitly", func(t *testing.T) {
		withCgroupMemoryLimit(t, 0, false)
		cfg := DefaultConfig()
		cfg.Auth.DevMode = true
		cfg.DownloadAdmission.MemoryBudgetBytes = 200 * 1024 * 1024

		err := cfg.Validate()
		if err == nil {
			t.Fatal("a 200 MiB budget validated")
		}
		if !strings.Contains(err.Error(), "memory_budget_percent does not change it") {
			t.Errorf("failure should say the percentage is inert for an explicit budget: %v", err)
		}
	})
}

// TestSmallContainerAdviceResolvesTheCase is the check the message itself
// cannot make: that the lever it names actually starts the node.
func TestSmallContainerAdviceResolvesTheCase(t *testing.T) {
	withCgroupMemoryLimit(t, 1<<30, true)
	cfg := DefaultConfig()
	cfg.Auth.DevMode = true
	cfg.DownloadAdmission.MemoryBudgetPercent = 33

	if err := cfg.Validate(); err != nil {
		t.Fatalf("raising memory_budget_percent to 33 on a 1 GiB container: %v", err)
	}
	if cfg.DownloadAdmission.MaxActivePerNode < 2 {
		t.Fatalf("derived %d slots", cfg.DownloadAdmission.MaxActivePerNode)
	}

	// And the alternative the message no longer offers really is a dead end.
	withCgroupMemoryLimit(t, 1<<30, true)
	blocked := DefaultConfig()
	blocked.Auth.DevMode = true
	blocked.DownloadAdmission.MemoryBudgetBytes = 330 * 1024 * 1024
	if err := blocked.Validate(); err == nil {
		t.Fatal("an explicit budget above the container share validated; the advice could have offered it")
	}
}

// TestSmallContainerAdviceStopsOfferingAnExhaustedLever covers the band where
// no share of the container is enough. Offering the percentage there is the
// same defect as offering an explicit budget on a 1 GiB node: following the
// advice produces either no change or a second refusal, this time about the
// setting's own range.
func TestSmallContainerAdviceStopsOfferingAnExhaustedLever(t *testing.T) {
	t.Run("no share can clear the floor", func(t *testing.T) {
		withCgroupMemoryLimit(t, 512*1024*1024, true)
		cfg := DefaultConfig()
		cfg.Auth.DevMode = true

		err := cfg.Validate()
		if err == nil {
			t.Fatal("a 512 MiB container validated")
		}
		if strings.Contains(err.Error(), "Raise download_admission.memory_budget_percent") {
			t.Errorf("advice offers a lever that cannot clear this floor at any share: %v", err)
		}
		if !strings.Contains(err.Error(), "even the maximum") {
			t.Errorf("advice does not say the share is exhausted: %v", err)
		}
	})

	t.Run("a larger share can still clear it", func(t *testing.T) {
		withCgroupMemoryLimit(t, 1<<30, true)
		cfg := DefaultConfig()
		cfg.Auth.DevMode = true

		err := cfg.Validate()
		if err == nil {
			t.Fatal("a 1 GiB container validated at the default share")
		}
		if !strings.Contains(err.Error(), "Raise download_admission.memory_budget_percent") {
			t.Errorf("advice withholds the lever that does clear this floor: %v", err)
		}
	})
}

// TestFloorAdviceNamesStreamLeverOnlyWhileTheBlockCostDominates covers both
// halves of a lever that was first unreachable and then unconditional.
//
// seafhttp.sync_block_max_bytes started as the initial value of the cost hint,
// which no input could reach: previewCost is streamCost plus a positive source
// and therefore always exceeds streamCost, so the raw cost could never equal the
// stream cost. Naming it always was the overcorrection — streamCost is
// max(plaintext peak, 4.5x block), so once the block size is below roughly
// 0.89 MiB the plaintext floor is what is charged and lowering the setting moves
// nothing. Validation only requires it to be positive, so that band is a
// configuration an operator can actually be in.
func TestFloorAdviceNamesStreamLeverOnlyWhileTheBlockCostDominates(t *testing.T) {
	adviceFor := func(t *testing.T, cfg *Config) string {
		t.Helper()
		streamCost, rawCost, err := downloadAdmissionMemoryCosts(cfg)
		if err != nil {
			t.Fatalf("costs: %v", err)
		}
		return downloadAdmissionFloorAdvice(cfg, cfg.DownloadAdmission, rawCost, streamCost, 0)
	}

	t.Run("named while the block cost is above the plaintext floor", func(t *testing.T) {
		for _, source := range []int64{1, 8, 32, 128} {
			for _, preview := range []int64{0, 50, 200, 900} {
				cfg := DefaultConfig()
				cfg.FileView.MaxIWorkSourceBytes = source * 1024 * 1024
				cfg.FileView.MaxIWorkPreviewBytes = preview * 1024 * 1024

				if advice := adviceFor(t, cfg); !strings.Contains(advice, "seafhttp.sync_block_max_bytes") {
					t.Errorf("source=%d preview=%d: advice omits the stream cost lever: %s", source, preview, advice)
				}
			}
		}
	})

	// 4 MiB / 4.5 is the exact block size at which the plaintext floor takes
	// over, so these two neighbours sit on either side of the transition.
	t.Run("withheld once the plaintext floor is what is charged", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			block int64
			want  bool
		}{
			{"one byte above the transition", 932068, true},
			{"one byte below the transition", 932067, false},
			{"512 KiB", 512 * 1024, false},
			{"the smallest accepted block", 1, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				cfg := DefaultConfig()
				cfg.SeafHTTP.SyncBlockMaxBytes = tc.block

				streamCost, _, err := downloadAdmissionMemoryCosts(cfg)
				if err != nil {
					t.Fatalf("costs: %v", err)
				}
				if got := streamCost > DownloadAdmissionPlaintextPeakBytes; got != tc.want {
					t.Fatalf("stream cost %d dominates=%v, want %v; the fixture no longer sits where it claims",
						streamCost, got, tc.want)
				}
				advice := adviceFor(t, cfg)
				if got := strings.Contains(advice, "seafhttp.sync_block_max_bytes"); got != tc.want {
					t.Errorf("block=%d: advice names the stream lever=%v, want %v: %s", tc.block, got, tc.want, advice)
				}
			})
		}
	})

	// The same withholding through the real startup path, so the assertion is
	// about the error an operator reads rather than about a helper's return.
	t.Run("withheld in the startup failure itself", func(t *testing.T) {
		withCgroupMemoryLimit(t, 512*1024*1024, true)
		cfg := DefaultConfig()
		cfg.Auth.DevMode = true
		cfg.SeafHTTP.SyncBlockMaxBytes = 512 * 1024

		err := cfg.Validate()
		if err == nil {
			t.Fatal("a 512 MiB container validated")
		}
		if !strings.Contains(err.Error(), "one stream slot (4194304 bytes)") {
			t.Fatalf("stream cost is not on its plaintext floor; the fixture no longer sits where it claims: %v", err)
		}
		if strings.Contains(err.Error(), "sync_block_max_bytes") {
			t.Errorf("advice offers a lever that cannot move a floored stream cost: %v", err)
		}
	})
}

// TestFloorAdviceWithholdsAnExhaustedShareForAnExplicitBudget is the configured
// counterpart of the cgroup case. An explicit budget is held to the same share
// of a detected container limit, so on a container too small for the maximum
// share, "raise memory_budget_bytes" is exactly as dead an end as raising the
// percentage — and it is the branch an operator reaches by pinning a budget on a
// small node, which is the likeliest way to arrive here.
func TestFloorAdviceWithholdsAnExhaustedShareForAnExplicitBudget(t *testing.T) {
	withCgroupMemoryLimit(t, 512*1024*1024, true)
	cfg := DefaultConfig()
	cfg.Auth.DevMode = true
	cfg.DownloadAdmission.MemoryBudgetBytes = 100 * 1024 * 1024

	err := cfg.Validate()
	if err == nil {
		t.Fatal("an explicit 100 MiB budget on a 512 MiB container validated")
	}
	if cfg.DownloadBudgetSource() != "configured" {
		t.Fatalf("budget source = %q; this test must exercise the configured branch", cfg.DownloadBudgetSource())
	}
	if strings.Contains(err.Error(), "Raise download_admission.memory_budget_bytes") {
		t.Errorf("advice offers a budget no share of this container can hold: %v", err)
	}
	if !strings.Contains(err.Error(), "cannot reach the floor") {
		t.Errorf("advice does not say the share is exhausted: %v", err)
	}

	// The same branch on a container with headroom must keep offering the budget
	// lever, or the gate has simply silenced the advice everywhere.
	//
	// A 512 MiB iWork source makes a raw slot 3072 MiB, so the floor is beyond
	// the default 25% of this container and reachable only within the 50%
	// ceiling. That is precisely the case the message describes: raise the
	// budget, "and raise that share too if one is in force". So the assertion is
	// not that the budget alone is sufficient — it is not — but that the advice
	// it gives is followable, which the second half proves by following it.
	withCgroupMemoryLimit(t, 8<<30, true)
	roomy := DefaultConfig()
	roomy.Auth.DevMode = true
	roomy.FileView.MaxIWorkSourceBytes = 512 * 1024 * 1024
	roomy.DownloadAdmission.MemoryBudgetBytes = 100 * 1024 * 1024
	err = roomy.Validate()
	if err == nil {
		t.Fatal("a 100 MiB budget against a 512 MiB iWork source validated")
	}
	if !strings.Contains(err.Error(), "Raise download_admission.memory_budget_bytes") {
		t.Errorf("advice withholds a budget lever this container can still satisfy: %v", err)
	}
	if strings.Contains(err.Error(), "cannot reach the floor") {
		t.Errorf("advice calls the share exhausted on a container that can clear the floor: %v", err)
	}

	withCgroupMemoryLimit(t, 8<<30, true)
	followed := DefaultConfig()
	followed.Auth.DevMode = true
	followed.FileView.MaxIWorkSourceBytes = 512 * 1024 * 1024
	followed.DownloadAdmission.MemoryBudgetPercent = MaxDownloadAdmissionMemoryBudgetPercent
	followed.DownloadAdmission.MemoryBudgetBytes = 4000 * 1024 * 1024
	if err := followed.Validate(); err != nil {
		t.Fatalf("following the advice — raise the budget and the share together — still refuses: %v", err)
	}
	if followed.DownloadAdmission.MaxActivePerNode < 2 {
		t.Fatalf("derived %d slots", followed.DownloadAdmission.MaxActivePerNode)
	}
}
