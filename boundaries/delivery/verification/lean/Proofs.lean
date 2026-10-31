/-
Invariants of the Delivery Offer state machine. `OfferMachine.lean` is generated
by Aeneas from `slices/delivery/domains/delivery/offer/machine.rs`, so the
theorems hold for the production code.
-/
import OfferMachine

open Aeneas.Std Result
open offer_machine machine

namespace Proofs

/-- The table has an outcome for every state and trigger, without a panic. -/
theorem outcome_total (s : OfferState) (t : OfferTrigger) :
    ∃ o, offer_outcome s t = ok o := by
  cases s <;> cases t <;> simp [offer_outcome]

/-- Only a pending offer can move. -/
theorem only_pending_moves {s target : OfferState} {t : OfferTrigger}
    (h : offer_outcome s t = ok (.Allowed target)) : s = .Pending := by
  cases s <;> cases t <;> simp_all [offer_outcome]

/-- Every allowed transition closes the offer. -/
theorem allowed_closes {s target : OfferState} {t : OfferTrigger}
    (h : offer_outcome s t = ok (.Allowed target)) : target.is_terminal = ok true := by
  cases s <;> cases t <;> simp_all [offer_outcome] <;> subst_vars <;>
    simp [OfferState.is_terminal, allowed, offer_outcome]

/-- A pending offer is the only state that is not terminal. -/
theorem pending_not_terminal : OfferState.Pending.is_terminal = ok false := by
  simp [OfferState.is_terminal, allowed, offer_outcome]

/-- No trigger leaves a terminal state. -/
theorem terminal_no_exit {s target : OfferState} (t : OfferTrigger)
    (h : s.is_terminal = ok true) : offer_outcome s t ≠ ok (.Allowed target) := by
  cases s <;> cases t <;>
    simp_all [OfferState.is_terminal, allowed, offer_outcome]

/-- Repeating the trigger that closed the offer is recognised as applied. -/
theorem repeat_already_applied {s target : OfferState} {t : OfferTrigger}
    (h : offer_outcome s t = ok (.Allowed target)) :
    offer_outcome target t = ok .AlreadyApplied := by
  cases s <;> cases t <;> simp_all [offer_outcome] <;> subst_vars <;> simp

/-- Any other trigger after the closing one is invalid: an accepted offer cannot
    expire or be withdrawn afterwards. -/
theorem other_trigger_invalid {s target : OfferState} {t t' : OfferTrigger}
    (h : offer_outcome s t = ok (.Allowed target)) (ne : t' ≠ t) :
    offer_outcome target t' = ok .Invalid := by
  cases s <;> cases t <;> cases t' <;> simp_all [offer_outcome] <;> subst_vars <;> simp

/-- A transition read from a stored state change is a transition of the table. -/
theorem applied_sound {s target : OfferState} {r : OfferTransitionResult}
    (h : applied_offer_transition s target = ok (some r)) :
    r.«from» = s ∧ r.«to» = target ∧ offer_outcome s r.trigger = ok (.Allowed target) := by
  cases s <;> cases target <;>
    simp_all [applied_offer_transition, leads_to, offer_outcome,
      OfferState.Insts.CoreCmpPartialEqOfferState.eq, OfferState.read_discriminant] <;>
    subst_vars <;> simp_all

/-- Every transition of the table is read back from its state change. -/
theorem applied_complete {s target : OfferState} {t : OfferTrigger}
    (h : offer_outcome s t = ok (.Allowed target)) :
    applied_offer_transition s target = ok (some { «from» := s, trigger := t, «to» := target }) := by
  cases s <;> cases t <;> simp_all [offer_outcome] <;> subst_vars <;>
    simp [applied_offer_transition, leads_to, offer_outcome,
      OfferState.Insts.CoreCmpPartialEqOfferState.eq, OfferState.read_discriminant]

end Proofs
