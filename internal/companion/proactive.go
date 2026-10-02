package companion

import "time"

// Proactive backoff.
//
// A message nobody answered is not a reason to send another one. Each
// unanswered message raises the wait before the next attempt, and past a
// ceiling she stops trying altogether until the user comes back on their own.
//
// Before this existed LastProactiveAt was written and never read, so every
// fifteen minute tick asked the model "should I message him" with total amnesia
// about the pile of messages already sitting unanswered. That is how thirty
// hours of silence turned into a dozen one-way messages, each one rediscovering
// that he had not replied.

// maxUnrepliedProactive is the point where she goes quiet. Beyond this there is
// nothing to say that another unanswered message will improve.
const maxUnrepliedProactive = 4

// proactiveBackoffGaps is indexed by the number of unanswered proactive
// messages. The zero at the front means an even conversation keeps its normal
// cadence; every step up is a longer silence she has to sit through first.
var proactiveBackoffGaps = []time.Duration{
	0,                // nothing outstanding -- speak when it feels right
	2 * time.Hour,    // one unanswered -- wait a real while
	5 * time.Hour,    // two -- only something genuinely worth saying
	12 * time.Hour,   // three -- once a day at most
}

// proactiveBackoff decides whether another proactive message may go out now,
// given how many are unanswered and when the last one was sent. It returns the
// remaining wait when the answer is no.
func proactiveBackoff(unreplied int, lastProactiveAt int64, now time.Time) (allowed bool, wait time.Duration) {
	if unreplied < 0 {
		unreplied = 0
	}
	if unreplied >= maxUnrepliedProactive {
		// She stops. Only the user reopening the conversation resets this.
		return false, 0
	}
	gap := proactiveBackoffGaps[unreplied]
	if gap <= 0 {
		return true, 0
	}
	if lastProactiveAt <= 0 {
		return true, 0
	}
	elapsed := now.Sub(time.UnixMilli(lastProactiveAt))
	if elapsed >= gap {
		return true, 0
	}
	return false, gap - elapsed
}
