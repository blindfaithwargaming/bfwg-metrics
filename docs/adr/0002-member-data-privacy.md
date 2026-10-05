# 0002 – Member data never enters this service

**Status:** accepted (2026-10-05)

## Context

The bot's state files and logs contain Discord user IDs, display names,
thread titles and free-text notes written by club members. That data is
private: it lives only on the bot's VM and in an access-controlled bucket. This
repository and its dashboard are public.

## Decision

1. **Drop at extract.** The ETL decoding structs declare only aggregate-safe fields
   (`internal/etl/games.go`). Identifiers are never decoded, so they cannot
   be stored or logged by mistake. The log parser matches IDs in its regex
   but does not capture them.
2. **Threshold at publish.** Faction names are member-typed free text. Factions
   with fewer than `BFWG_MIN_FACTION_GAMES` games (default 3) are
   withheld, so a one-off entry cannot identify who played it.
3. **Synthetic fixtures only.** `testdata/` is generated, and real exports go in
   the git-ignored `private/` directory.
4. **Enforced by tests.** `TestMemberDataNeverReachesDatabase` scans every
   stored value, and `TestPublicOutputContainsNoMemberData` scans every public
   response, for identifiers and free text planted in the fixtures.

## Consequences

Per-player statistics such as leaderboards and individual records are out of scope for this
service. If the club ever wants them, they belong in the bot, behind Discord's
access controls, not on a public page.
