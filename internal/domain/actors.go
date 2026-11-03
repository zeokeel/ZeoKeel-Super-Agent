package domain

// Identity and event-time helpers shared by every Source. They exist so the three
// layers that produce or read a Trigger (Source drivers, the engine, the panel)
// agree on one definition instead of each re-deriving it.

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// OperatorActorID is the identity the operator's own messages carry. A Source
// that registers the operator's personal account maps that account to this id, so
// "who created this" is a stable fact rather than a per-call guess.
const OperatorActorID = "operator"

// MinEventTime is the oldest timestamp a real event can carry. A driver that
// cannot read a message's time must leave At zero (which EnsureAt fills with
// now); an event stamped at the Unix epoch or in 1970 is a bug, not history, and
// treating it as valid would put it outside every Eval deadline and every
// ordering window.
var MinEventTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// maxClockSkew is how far an event may claim to be in the future before the
// timestamp is treated as a bad clock rather than a real event.
const maxClockSkew = 24 * time.Hour

// IsOperatorMessage reports whether a Trigger was created by the operator
// themselves. The semantics are deliberately fact-based, not a Source-name check:
//
//   - Self: the Source recognised the operator's own account (DingTalk/Feishu/
//     WeChat personal id, a bound account, the panel's operator actor);
//   - the actor is OperatorActorID on any Source;
//   - a local (panel) message with no explicit actor: the panel has no other
//     author, so its default author is the operator.
//
// "Source == local" alone is NOT the definition: a local Source could carry a
// forwarded colleague message, and a GitHub/IM message sent from the operator's
// own account must still count as theirs.
func (t Trigger) IsOperatorMessage() bool {
	if t.Self || t.Actor.ID == OperatorActorID {
		return true
	}
	return t.Source == "local" && t.Actor.ID == ""
}

// CreatedByMe is the readable alias of IsOperatorMessage for call sites that ask
// the question from the UI's point of view ("this conversation is mine").
func (t Trigger) CreatedByMe() bool { return t.IsOperatorMessage() }

// IsOperatorGrant reports whether a Mandate was granted on the operator's own
// authority (their own request, on any Source).
func (m Mandate) IsOperatorGrant() bool {
	return m.Basis.Via == ViaOperator || m.Actor.ID == OperatorActorID
}

// CreatedByOperator reports whether the work was started by the operator, which
// is what a workspace conversation page filters on. It reads the Mandate (the
// authorization fact), not the last message, so a colleague's follow-up does not
// turn an operator-created Ticket into a colleague-created one.
func (t Ticket) CreatedByOperator() bool { return t.Mandate.IsOperatorGrant() }

// EnsureAt replaces a missing or implausible event timestamp with now and reports
// whether it changed. A zero timestamp would sort a message to the beginning of
// every Thread; a 1970 one would do the same and escape the Eval deadline. Drivers
// call it when they translate a raw event, before the Trigger enters the domain.
func (t *Trigger) EnsureAt(now time.Time) bool {
	if now.IsZero() {
		now = time.Now()
	}
	changed := t.At.IsZero() || t.At.Before(MinEventTime) || t.At.After(now.Add(maxClockSkew))
	if changed {
		t.At = now.UTC()
	}
	return changed
}

// Validate checks the facts every Trigger must carry before it can be received.
// It is a guard for Source drivers and tests, not a gate: admissibility is the
// engine's deterministic gate.
func (t Trigger) Validate() error {
	if t.ID == "" {
		return errors.New("domain: trigger id is required")
	}
	if strings.TrimSpace(t.Source) == "" {
		return errors.New("domain: trigger source is required")
	}
	if strings.Contains(t.Source, "|") {
		return fmt.Errorf("domain: trigger source %q must not contain the Thread separator", t.Source)
	}
	if t.At.IsZero() || t.At.Before(MinEventTime) {
		return fmt.Errorf("domain: trigger %s has no usable timestamp (got %s)", t.ID, t.At)
	}
	if t.At.After(time.Now().Add(maxClockSkew)) {
		return fmt.Errorf("domain: trigger %s claims a timestamp too far in the future (%s)", t.ID, t.At)
	}
	if len(t.Threads) == 0 {
		return fmt.Errorf("domain: trigger %s belongs to no Thread", t.ID)
	}
	for _, ref := range t.Threads {
		if strings.TrimSpace(ref.Source) == "" || strings.TrimSpace(ref.ID) == "" {
			return fmt.Errorf("domain: trigger %s has an incomplete Thread reference %+v", t.ID, ref)
		}
	}
	if t.Human && strings.TrimSpace(t.Actor.ID) == "" {
		return fmt.Errorf("domain: human trigger %s has no actor", t.ID)
	}
	return nil
}
