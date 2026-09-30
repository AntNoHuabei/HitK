package companion

import (
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
)

// IntensityAt returns the effect multiplier at a point in time.
func (e ActiveEffect) IntensityAt(now int64) float64 {
	if now < e.StartAt || now >= e.EndAt || e.EndAt <= e.StartAt {
		return 0
	}
	progress := float64(now-e.StartAt) / float64(e.EndAt-e.StartAt)
	switch strings.ToLower(e.Curve) {
	case "linear_decay", "ramp_decay":
		return 1 - progress
	case "ramp":
		if progress < 0.2 {
			return progress / 0.2
		}
		return 1
	case "pulse":
		if progress < 0.35 {
			return 1
		}
		return math.Max(0, 1-(progress-0.35)/0.65)
	default:
		return 1
	}
}

func activeEffects(rule EventRule, now time.Time) []ActiveEffect {
	out := make([]ActiveEffect, 0, len(rule.Effects))
	for _, effect := range rule.Effects {
		start := now.Add(effect.Delay.Std())
		end := start.Add(effect.Duration.Std())
		out = append(out, ActiveEffect{
			ID:            "eff_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12],
			SourceRuleID:  rule.ID,
			Target:        effect.Target,
			StartAt:       start.UnixMilli(),
			EndAt:         end.UnixMilli(),
			RatePerHour:   effect.RatePerHour,
			BaselineDelta: effect.BaselineDelta,
			RecoveryDelta: effect.RecoveryDelta,
			Curve:         effect.Curve,
			Stacking:      effect.Stacking,
		})
	}
	return out
}

func mergeEffects(existing, added []ActiveEffect) []ActiveEffect {
	out := append([]ActiveEffect(nil), existing...)
	for _, effect := range added {
		replaced := false
		for i := range out {
			if out[i].SourceRuleID == effect.SourceRuleID && out[i].Target == effect.Target {
				switch strings.ToLower(effect.Stacking) {
				case "add":
					out = append(out, effect)
				case "cap":
					if out[i].RatePerHour < effect.RatePerHour {
						out[i] = effect
					}
				default: // refresh/replace
					out[i] = effect
				}
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, effect)
		}
	}
	return out
}

func pruneEffects(effects []ActiveEffect, now int64) []ActiveEffect {
	out := effects[:0]
	for _, effect := range effects {
		if effect.EndAt > now {
			out = append(out, effect)
		}
	}
	return out
}
