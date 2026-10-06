# world-resolve

Fixture world for the resolver tests. It is not an example for writers and not
a validator test (that is `testdata/world-ok/`). IDs here are consumed by the
resolver tests; do not rename them.

Two decisions: `dec_vale` (held, fell) and `dec_heir` (named, hidden). Only
`loc_vale_keep` and its edges are conditional on `held`; only
`stmt_heir_sworn_fell` and its belief are conditional on `fell`. Diff(held,
fell) is exactly: held-only = `loc_vale_keep`, its `located_in` edge to
`loc_vale`, and the unconditional edge `obj_horn -> loc_vale_keep` (it hides
when its target does); fell-only = `stmt_heir_sworn_fell` and Vesk's belief on it.

## Decisions

- `dec_vale`: outcomes held, fell. Condition target for the Vale branch.
- `dec_heir`: outcomes named, hidden. Condition target for Ward's membership.

## Factions

- `fac_court`: knower. Believes `stmt_heir_lives` true and `stmt_miren_oath` true.
- `fac_order`: knower with no memberships. Believes `stmt_heir_lives` false.
- `fac_veil`: holds a belief (`stmt_veil_secret` true); target of Ward's conditional membership.
- `fac_draft` (draft): has a canon member (`char_draftling`) and a belief (`stmt_heir_lives` true).

## Characters

- `char_kaelen`: exactly two edges, `member_of fac_court` (priority 1) and
  `member_of fac_order` (priority 2). No beliefs, no assertions. Inherits the
  court's view of `stmt_heir_lives` (true) by priority, and the court's
  `stmt_miren_oath` (true).
- `char_heir`: subject of the contested statements. No edges of its own.
- `char_miren`: the heretic. Member of `fac_court` only; believes
  `stmt_miren_oath` false against the court's true.
- `char_vesk`: the liar. Believes `stmt_vesk_sworn` true, asserts it false.
  Also believes `stmt_heir_sworn_fell` true. Two-way edge: `sibling_of char_conformist`.
- `char_ward`: only membership is `member_of fac_veil` valid in
  `dec_heir=hidden`. No beliefs of his own; the Veil's belief on
  `stmt_veil_secret` is his only source, and only when the heir is hidden.
- `char_conformist`: no divergence. Member of `fac_court`, no beliefs of her
  own, asserts nothing; inherits only values equal to canon truth. Also the
  source of the one `mentions` edge (`-> char_heir`).
- `char_draftling`: canon member of the draft faction `fac_draft`.

## Locations and objects

- `loc_vale`: unconditional place.
- `loc_vale_keep`: valid_in `dec_vale=held`; its own `located_in loc_vale`
  edge carries the same condition.
- `obj_horn`: unconditional, `located_in loc_vale_keep`. The inverse
  (`contains`) is derived, so the keep gains an inbound edge from an
  unconditional entity through a relation with a declared inverse.

## Statements: canon truth and believers

| statement | subject / relation / object | truth | exercises |
|---|---|---|---|
| `stmt_heir_lives` | char_heir heir_of fac_court | true | Opposite faction beliefs: court true (matches canon), order false. Kaelen inherits by priority. Draft faction also believes true. |
| `stmt_miren_oath` | char_miren sworn_to fac_court | true | Heretic: court true, Miren false (Miren is wrong). |
| `stmt_vesk_sworn` | char_vesk sworn_to fac_court | true | Liar: Vesk believes true, asserts false. |
| `stmt_heir_common` | char_heir sworn_to fac_court | true | `common: true`; believed and asserted by no one. |
| `stmt_heir_sworn_fell` | char_heir sworn_to fac_order | true | valid_in `dec_vale=fell`; belief held by Vesk (not Kaelen). |
| `stmt_veil_secret` | char_heir sworn_to fac_veil | true | Belief held only by `fac_veil`; reaches Ward only under `dec_heir=hidden`. |

Agents' beliefs: court: heir_lives true, miren_oath true. order: heir_lives
false. veil: veil_secret true. draft: heir_lives true. Miren: miren_oath false.
Vesk: vesk_sworn true, heir_sworn_fell true. Nobody else holds beliefs of their own.

## Intended warnings (exactly these two)

- `canon_depends_on_draft` in `world/characters/draftling.md` (member of the draft faction).
- `unbelieved_statement` in `world/statements/heir-common.md` (the common statement).

No orphans, no `inherited_belief_conflict`.
