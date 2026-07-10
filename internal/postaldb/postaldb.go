package postaldb

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/tipmarket/swift-ai/internal/core"

	_ "modernc.org/sqlite"
)

const defaultMaxMatchesPerPostcode = 50

type Runner struct {
	db                    *sql.DB
	maxMatchesPerPostcode int
}

type Evidence struct {
	InputCountry      string
	InputTown         string
	InputPostcode     string
	PostcodeExists    bool
	CountryMatches    bool
	TownMatches       bool
	SafeTownEvidence  bool
	HasCoordinates    bool
	PostalOnlyMatches bool
	Locations         []Location
}

type Location struct {
	Country      string
	Town         string
	Postcode     string
	Latitude     sql.NullFloat64
	Longitude    sql.NullFloat64
	Accuracy     sql.NullInt64
	Coordinates  sql.NullString
	Source       string
	DataAddrRows int
	PostalRows   int
}

type postcodeCandidate struct {
	code  string
	start int
	end   int
}

func Open(path string) (*Runner, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open postal db: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postal db: %w", err)
	}
	return &Runner{
		db:                    db,
		maxMatchesPerPostcode: defaultMaxMatchesPerPostcode,
	}, nil
}

func (r *Runner) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

func (r *Runner) Validate(ctx context.Context, country string, town string, postcode string) (Evidence, error) {
	evidence := Evidence{
		InputCountry:  normalizeValue(country),
		InputTown:     normalizeValue(town),
		InputPostcode: normalizeValue(postcode),
	}
	if evidence.InputPostcode == "" {
		return evidence, nil
	}

	locations, err := r.lookup(ctx, evidence.InputPostcode, evidence.InputCountry, 0)
	if err != nil {
		return Evidence{}, err
	}
	evidence.Locations = locations
	evidence.PostcodeExists = len(locations) > 0

	for _, location := range locations {
		if location.Country == evidence.InputCountry {
			evidence.CountryMatches = true
		}
		if location.Latitude.Valid && location.Longitude.Valid {
			evidence.HasCoordinates = true
		}
		if location.Source == "postal_codes" {
			evidence.PostalOnlyMatches = true
		}
		if evidence.InputTown != "" && location.Town == evidence.InputTown {
			evidence.TownMatches = true
			if location.Source == "both_town" {
				evidence.SafeTownEvidence = true
			}
		}
	}

	return evidence, nil
}

func (r *Runner) MatchBatch(ctx context.Context, data []string) ([][]core.PostcodeMatch, error) {
	results := make([][]core.PostcodeMatch, len(data))
	for i, text := range data {
		candidates := extractPostcodeCandidates(text)
		matches := make([]core.PostcodeMatch, 0)
		seen := make(map[string]struct{})
		for _, candidate := range candidates {
			countries, err := r.lookupCountryEvidence(ctx, candidate.code)
			if err != nil {
				return nil, err
			}
			for _, country := range countries {
				key := country + "\x00\x00" + candidate.code
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				matches = append(matches, core.PostcodeMatch{
					Start:   candidate.start,
					End:     candidate.end,
					Matched: candidate.code,
					Origin:  country,
				})
			}

			locations, err := r.lookupSafeTownEvidence(ctx, candidate.code)
			if err != nil {
				return nil, err
			}
			for _, location := range locations {
				key := location.Country + "\x00" + location.Town + "\x00" + candidate.code
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				matches = append(matches, core.PostcodeMatch{
					Start:       candidate.start,
					End:         candidate.end,
					Matched:     candidate.code,
					Possibility: location.Town,
					Origin:      location.Country,
				})
			}
		}
		results[i] = matches
	}
	return results, nil
}

func (r *Runner) lookupCountryEvidence(ctx context.Context, postcode string) ([]string, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("postal db runner is nil")
	}
	postcode = normalizeValue(postcode)
	if postcode == "" {
		return nil, nil
	}

	rows, err := r.db.QueryContext(ctx, `
SELECT DISTINCT country
FROM locations
WHERE postcode = ? AND country <> ''
ORDER BY country
LIMIT ?`, postcode, r.maxMatchesPerPostcode)
	if err != nil {
		return nil, fmt.Errorf("query postal country evidence: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var countries []string
	for rows.Next() {
		var country string
		if err := rows.Scan(&country); err != nil {
			return nil, fmt.Errorf("scan postal country evidence: %w", err)
		}
		countries = append(countries, country)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postal country evidence: %w", err)
	}
	return countries, nil
}

func (r *Runner) lookupSafeTownEvidence(ctx context.Context, postcode string) ([]Location, error) {
	return r.lookup(ctx, postcode, "", r.maxMatchesPerPostcode)
}

func (r *Runner) lookup(ctx context.Context, postcode string, country string, limit int) ([]Location, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("postal db runner is nil")
	}
	postcode = normalizeValue(postcode)
	country = normalizeValue(country)
	if postcode == "" {
		return nil, nil
	}

	query := `
SELECT country, town, postcode, latitude, longitude, accuracy, coordinates, source, dataaddr_rows, postal_rows
FROM locations
WHERE postcode = ?`
	args := []any{postcode}
	if country != "" {
		query += ` AND country = ?`
		args = append(args, country)
	}
	if limit > 0 {
		query += ` AND source = 'both_town' AND town IS NOT NULL ORDER BY dataaddr_rows DESC, postal_rows DESC LIMIT ?`
		args = append(args, limit)
	} else {
		query += ` ORDER BY source = 'both_town' DESC, dataaddr_rows DESC, postal_rows DESC`
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query postal db: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var locations []Location
	for rows.Next() {
		var location Location
		var town sql.NullString
		var postcode sql.NullString
		var latitude any
		var longitude any
		var accuracy any
		if err := rows.Scan(
			&location.Country,
			&town,
			&postcode,
			&latitude,
			&longitude,
			&accuracy,
			&location.Coordinates,
			&location.Source,
			&location.DataAddrRows,
			&location.PostalRows,
		); err != nil {
			return nil, fmt.Errorf("scan postal location: %w", err)
		}
		if town.Valid {
			location.Town = town.String
		}
		if postcode.Valid {
			location.Postcode = postcode.String
		}
		var err error
		location.Latitude, err = parseNullFloat64(latitude)
		if err != nil {
			return nil, fmt.Errorf("parse postal latitude: %w", err)
		}
		location.Longitude, err = parseNullFloat64(longitude)
		if err != nil {
			return nil, fmt.Errorf("parse postal longitude: %w", err)
		}
		location.Accuracy, err = parseNullInt64(accuracy)
		if err != nil {
			return nil, fmt.Errorf("parse postal accuracy: %w", err)
		}
		locations = append(locations, location)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postal locations: %w", err)
	}
	return locations, nil
}

func parseNullFloat64(value any) (sql.NullFloat64, error) {
	switch v := value.(type) {
	case nil:
		return sql.NullFloat64{}, nil
	case float64:
		return sql.NullFloat64{Float64: v, Valid: true}, nil
	case int64:
		return sql.NullFloat64{Float64: float64(v), Valid: true}, nil
	case string:
		return parseNullFloat64String(v)
	case []byte:
		return parseNullFloat64String(string(v))
	default:
		return sql.NullFloat64{}, fmt.Errorf("unsupported value type %T", value)
	}
}

func parseNullFloat64String(value string) (sql.NullFloat64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return sql.NullFloat64{}, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return sql.NullFloat64{}, err
	}
	return sql.NullFloat64{Float64: parsed, Valid: true}, nil
}

func parseNullInt64(value any) (sql.NullInt64, error) {
	switch v := value.(type) {
	case nil:
		return sql.NullInt64{}, nil
	case int64:
		return sql.NullInt64{Int64: v, Valid: true}, nil
	case float64:
		return sql.NullInt64{Int64: int64(v), Valid: true}, nil
	case string:
		return parseNullInt64String(v)
	case []byte:
		return parseNullInt64String(string(v))
	default:
		return sql.NullInt64{}, fmt.Errorf("unsupported value type %T", value)
	}
}

func parseNullInt64String(value string) (sql.NullInt64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return sql.NullInt64{}, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return sql.NullInt64{}, err
	}
	return sql.NullInt64{Int64: parsed, Valid: true}, nil
}

func extractPostcodeCandidates(text string) []postcodeCandidate {
	var candidates []postcodeCandidate
	seen := make(map[string]struct{})
	start := -1
	var b strings.Builder

	flush := func(end int) {
		if b.Len() == 0 {
			start = -1
			return
		}
		code := b.String()
		if len(code) >= 3 && len(code) <= 10 {
			if _, ok := seen[code]; !ok {
				seen[code] = struct{}{}
				candidates = append(candidates, postcodeCandidate{
					code:  code,
					start: start,
					end:   end,
				})
			}
		}
		b.Reset()
		start = -1
	}

	for offset, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if start < 0 {
				start = offset
			}
			b.WriteRune(unicode.ToUpper(r))
			continue
		}
		flush(offset)
	}
	flush(len(text))

	return candidates
}

func normalizeValue(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}
