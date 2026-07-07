// Package biztime centralizes business-calendar time rules.
//
// Time policy:
//   - Database timestamps are stored as timestamptz and treated as absolute
//     instants.
//   - SQL now() is acceptable for technical timestamps such as created_at,
//     updated_at, paid_at, and queue status timestamps.
//   - Calendar business ranges such as "today", "yesterday", "per day", and
//     daily statistics must be calculated with DayBounds and the configured
//     business timezone.
//   - Delays such as "after 24 hours" or "after 7 days" stay as durations from
//     the source event when the product rule is elapsed time, not a calendar day.
//   - Retention and technical schedules may stay in UTC.
package biztime
