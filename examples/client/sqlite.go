package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

type addressRecord struct {
	ID      string
	Address string
	Town    string
	Country string
	Stratum string
}

func openSQLite(path string) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("sqlite path is required")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite %q: %w", path, err)
	}
	return db, nil
}

func loadTownGroups(ctx context.Context, db *sql.DB, countryFilter string, includeBlankTown bool) ([]townGroup, error) {
	if db == nil {
		return nil, errors.New("sqlite db is nil")
	}
	var args []string
	where := []string{"country IS NOT NULL", "trim(country) <> ''"}
	townExpr := "coalesce(town, '')"
	if !includeBlankTown {
		townExpr = "town"
		where = append(where, "town IS NOT NULL", "town <> ''")
	}
	if country := canonicalCountry(countryFilter); country != "" {
		where = append(where, "country = ?")
		args = append(args, dbCountry(country))
	}
	query := `
SELECT upper(country) AS country, ` + townExpr + ` AS town, count(*) AS count
FROM addresses
WHERE ` + strings.Join(where, " AND ") + `
GROUP BY country, ` + townExpr + `
ORDER BY country, count DESC, town`

	rows, err := db.QueryContext(ctx, query, stringArgs(args)...)
	if err != nil {
		return nil, fmt.Errorf("query town groups: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var groups []townGroup
	for rows.Next() {
		var group townGroup
		if err := rows.Scan(&group.Country, &group.Town, &group.Count); err != nil {
			return nil, fmt.Errorf("scan town group: %w", err)
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate town groups: %w", err)
	}
	return groups, nil
}

func fetchAddressAtPlanOffset(ctx context.Context, db *sql.DB, plan samplePlan) (addressRecord, error) {
	if db == nil {
		return addressRecord{}, errors.New("sqlite db is nil")
	}
	if plan.Country == "" {
		return addressRecord{}, errors.New("sample country is required")
	}
	query := `
SELECT id, address, coalesce(town, '') AS town, upper(country) AS country
FROM addresses
WHERE country = ? AND town = ?
ORDER BY id
LIMIT 1 OFFSET ?`
	row := db.QueryRowContext(ctx, query, dbCountry(plan.Country), plan.Town, plan.Offset)

	record := addressRecord{Stratum: plan.Stratum}
	if err := row.Scan(&record.ID, &record.Address, &record.Town, &record.Country); err != nil {
		return addressRecord{}, fmt.Errorf("fetch address country=%s town=%q offset=%d: %w", plan.Country, plan.Town, plan.Offset, err)
	}
	return record, nil
}

func loadSampledAddresses(ctx context.Context, db *sql.DB, plans []samplePlan) ([]addressRecord, error) {
	records := make([]addressRecord, 0, len(plans))
	seen := map[string]bool{}
	for _, plan := range plans {
		record, err := fetchAddressAtPlanOffset(ctx, db, plan)
		if err != nil {
			return nil, err
		}
		if seen[record.ID] {
			continue
		}
		seen[record.ID] = true
		records = append(records, record)
	}
	return records, nil
}

func stringArgs(values []string) []any {
	args := make([]any, len(values))
	for i, value := range values {
		args[i] = value
	}
	return args
}

func dbCountry(country string) string {
	return strings.ToLower(canonicalCountry(country))
}
