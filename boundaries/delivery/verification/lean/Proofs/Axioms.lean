/-
Fails the build if a theorem under `Proofs` depends on anything but the standard
axioms of Lean: `sorry` shows up as `sorryAx`, `native_decide` as a generated axiom,
and a declared `axiom` under its own name. New theorems are covered without listing them.
-/
import Lean
import Proofs.Offer
import Proofs.Delivery

open Lean Elab Command

namespace Proofs.Axioms

def standard : List Name := [``propext, ``Classical.choice, ``Quot.sound]

run_cmd do
  let theorems := (← getEnv).constants.fold (init := #[]) fun found decl info =>
    match info with
    | .thmInfo _ => if (`Proofs).isPrefixOf decl then found.push decl else found
    | _ => found
  if theorems.isEmpty then
    throwError "no theorems found under Proofs"
  for decl in theorems do
    let foreign := (← collectAxioms decl).filter (!standard.contains ·)
    unless foreign.isEmpty do
      logError m!"{decl} depends on {foreign.toList}"

end Proofs.Axioms
