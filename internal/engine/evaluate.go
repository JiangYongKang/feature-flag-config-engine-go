package engine

import (
	"log"
	"strconv"

	"github.com/highcumontoa/feature-flag-config-engine-go/internal/domain"
)

// evaluate runs a flag decision against one immutable snapshot.
//
// Determinism guarantees: rules are evaluated in declared order (first
// match wins); percentage gating uses a stable FNV-1a hash over
// (flag, rule, bucket_by value), so identical input yields identical
// output on repeat calls and after process restarts.
func (e *Engine) evaluate(s *snapshot, key string, attrs domain.Attributes, overrides map[string]any) (*domain.Decision, error) {
	flag, ok := s.cfg.Flags[key]
	if !ok {
		return nil, domain.Err(domain.ReasonUnknownFlag,
			"no source defines flag %q", key)
	}
	srcName := s.cfg.FlagProvenance[key]
	d := &domain.Decision{
		Flag:   key,
		Source: srcName,
		Trace:  []domain.TraceStep{},
	}
	d.Trace = append(d.Trace, domain.TraceStep{
		Step:   "resolve_source",
		Detail: "flag taken from highest-priority source defining it",
		Source: srcName,
	})

	// 1. Reject malformed request attributes up front: unsupported types
	//    must not be coerced silently.
	for ak, av := range attrs {
		if _, ok := attrString(av); !ok {
			return nil, domain.Err(domain.ReasonInvalidAttribute,
				"attribute %q has unsupported type %T (want string|bool|number|null)",
				ak, av)
		}
	}

	// 2. Authorization gate for evaluation-time overrides. A flag may be
	//    force-set only if its own key is in allow_override; overrides for
	//    any other key are denied with a distinguishable reason.
	if v, forced := overrides[key]; forced {
		if !contains(flag.AllowOverride, key) {
			return nil, domain.Err(domain.ReasonOverrideDenied,
				"flag %q does not permit evaluation-time override (allow_override=%v)",
				key, flag.AllowOverride)
		}
		b, isBool := v.(bool)
		if !isBool {
			return nil, domain.Err(domain.ReasonInvalidAttribute,
				"override for flag %q must be bool, got %T", key, v)
		}
		d.Reason = "override"
		d.Variation = boolVariation(b)
		d.Value = b
		d.Trace = append(d.Trace, domain.TraceStep{
			Step:    "override",
			Detail:  "authorized evaluation-time override applied",
			Matched: true,
		})
		logDecision(d, attrs)
		return d, nil
	}
	// Any override targeting a different/unknown key is an authorization error.
	for ok := range overrides {
		if ok != key {
			return nil, domain.Err(domain.ReasonOverrideDenied,
				"override of %q is not allowed while evaluating flag %q", ok, key)
		}
	}

	// 3. Ordered rule evaluation; first fully-matching rule wins.
	for _, r := range flag.Rules {
		hit, frag, err := matchRule(r, attrs)
		if err != nil {
			return nil, err
		}
		if !hit {
			d.Trace = append(d.Trace, domain.TraceStep{
				Step:    "rule",
				Detail:  "rule " + r.Name + " skipped: condition false (" + frag + ")",
				Matched: false,
			})
			continue
		}

		// Percentage gate (stable bucketing).
		if r.Percentage < 100 {
			bv, present := attrs[r.BucketBy]
			if !present {
				d.Trace = append(d.Trace, domain.TraceStep{
					Step:    "rule",
					Detail:  "rule " + r.Name + " condition matched but bucket_by attribute " + r.BucketBy + " absent; falling through",
					Matched: false,
				})
				continue
			}
			bs, _ := attrString(bv)
			sum, bkt := bucket(key, r.Name, bs)
			inBucket := bkt < r.Percentage
			d.Trace = append(d.Trace, domain.TraceStep{
				Step: "rule",
				Detail: "rule " + r.Name + " condition matched (" + frag + "); bucket=" +
					strconv.Itoa(bkt) + " threshold=" + strconv.Itoa(r.Percentage) +
					" in_bucket=" + strconv.FormatBool(inBucket),
				Matched:    inBucket,
				BucketHash: sum,
			})
			if !inBucket {
				continue
			}
		} else {
			d.Trace = append(d.Trace, domain.TraceStep{
				Step:    "rule",
				Detail:  "rule " + r.Name + " condition matched (" + frag + "); serve=100%",
				Matched: true,
			})
		}

		d.Reason = "rule:" + r.Name
		d.Variation = r.Variation
		d.Value = variationBool(r.Variation)
		logDecision(d, attrs)
		return d, nil
	}

	// 4. Deterministic fallback to the declared default.
	d.Reason = "default"
	d.Variation = flag.DefaultVariation
	d.Value = variationBool(flag.DefaultVariation)
	d.Trace = append(d.Trace, domain.TraceStep{
		Step:    "default",
		Detail:  "no rule matched; serving declared default_variation",
		Matched: true,
	})
	logDecision(d, attrs)
	return d, nil
}

// variationBool interprets conventional variation names as booleans.
// "on"/"true"/"enabled" => true; everything else ("off", ...) => false.
func variationBool(v string) bool {
	switch v {
	case "on", "true", "enabled":
		return true
	default:
		return false
	}
}

func boolVariation(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// logDecision prints the input and the exact basis for the verdict, as
// required for explainable test/run logs.
func logDecision(d *domain.Decision, attrs domain.Attributes) {
	log.Printf("[flag-eval] flag=%s attrs=%v -> variation=%q value=%t reason=%s source=%s",
		d.Flag, attrs, d.Variation, d.Value, d.Reason, d.Source)
	for i, t := range d.Trace {
		log.Printf("[flag-eval]   trace[%d] %s matched=%v %s", i, t.Step, t.Matched, t.Detail)
	}
}
