---
id: char_miren
type: character
name: Miren
aliases: ["The Quiet Envoy"]
status: canon
visibility: public
lifespan: { era: third_reign, earliest: 375, latest: 421, precision: approximate }
relations:
  - { type: member_of, target: fac_ashen_court, priority: 1 }
  - { type: participated_in, target: evt_siege_of_vale }
beliefs:
  - { statement: stmt_orrin_oath, value: false, confidence: high }
asserts:
  - { statement: stmt_orrin_oath, value: true, to: char_kaelen_vor,
      when: { era: third_reign, earliest: 412 } }
---

Miren knows what the oath was worth and says the opposite. The graph records
both, and the pair of them is the lie.
