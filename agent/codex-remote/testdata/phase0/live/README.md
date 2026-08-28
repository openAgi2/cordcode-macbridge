# Live fixture landing zone

This directory intentionally contains no live fixture yet.

Only a redacted observation set produced from the exact frozen target may be committed here. The set must satisfy `../live-fixture-contract.json`, use fixture-local pseudonyms, remove all user content and secrets before repository write, and include proof that the probe controller alone was revoked during cleanup.

Raw captures, authentication material, pairing/MFA values, signatures and account-correlatable identifiers must never enter this repository. A static schema fixture or a locally fabricated envelope cannot fill this directory or satisfy Gate P0.
