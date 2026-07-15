// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package questdb

import (
	"context"
	"fmt"
	"strings"
)

// DownsampleStmt builds an idempotent INSERT … SELECT aggregation that rolls
// `src` up into `dst` at the given rollup (e.g. "1m", "1h"), covering the
// window [from, to). QuestDB's SAMPLE BY performs the time bucketing; FILL
// (NULL) leaves gaps explicit rather than forward-filling.
//
// The window bounds are passed as QuestDB date expressions (e.g.
// "dateadd('d', -1, now())") so callers can schedule overlapping windows for
// at-least-once rollups.
func DownsampleStmt(rollup, src, dst, from, to string) string {
	return fmt.Sprintf(
		`INSERT INTO %s
SELECT timestamp_floor('%s', ts) ts, device, sensor, metric,
       avg(value) avg, min(value) min, max(value) max, count(value) count
FROM %s
WHERE ts >= %s AND ts < %s
SAMPLE BY %s FILL(NULL)`,
		dst, rollup, src, from, to, rollup,
	)
}

// RetentionDropStmt builds an ALTER TABLE DROP PARTITION statement that evicts
// partitions entirely older than the given QuestDB date expression (e.g.
// "dateadd('d', -90, now())"). Dropping whole partitions is far cheaper than
// row-level deletes in a time-series store.
func RetentionDropStmt(table, olderThan string) string {
	return fmt.Sprintf("ALTER TABLE %s DROP PARTITION WHERE ts < %s", table, olderThan)
}

// MaintenancePlan returns the ordered SQL statements for one retention and
// downsampling pass. rawRetention is the date expression for aging out the raw
// `readings` table (e.g. "dateadd('d', -90, now())"); the rollups are kept
// longer via rollupRetention (e.g. "dateadd('y', -2, now())").
//
// Downsampling windows are expressed relative to now() so the same plan can be
// re-run safely on a schedule (idempotent thanks to DEDUP/upsert semantics).
func MaintenancePlan(rawRetention, rollupRetention string) []string {
	stmts := []string{
		// Rollups first so the source data still exists if a rollup fails.
		DownsampleStmt("1m", "readings", "readings_1m", "dateadd('d', -2, now())", "dateadd('d', -1, now())"),
		DownsampleStmt("1h", "readings_1m", "readings_1h", "dateadd('d', -3, now())", "dateadd('d', -1, now())"),
		// Then age out raw and rollup partitions.
		RetentionDropStmt("readings", rawRetention),
		RetentionDropStmt("readings_1m", rollupRetention),
	}
	return stmts
}

// RunMaintenance executes the maintenance plan and reports which statements
// ran. It stops at the first error.
func (c *Client) RunMaintenance(ctx context.Context, rawRetention, rollupRetention string) error {
	return c.ExecMany(ctx, MaintenancePlan(rawRetention, rollupRetention)...)
}

// sanitizeIdent is a tiny guard so table/rollup names from config cannot
// smuggle SQL. Callers pass constants today, but defense-in-depth is cheap.
func sanitizeIdent(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return -1
	}, s)
}
