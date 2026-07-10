package cache

import (
	"context"
	"testing"

	"github.com/tipmarket/swift-ai/internal/structured"
)

func TestRedisDocIDIsStableSHA256Hex(t *testing.T) {
	got := redisDocID("77 rue de rivoli 75001 paris")
	want := "6f6e2745b8d70c807e9742de7fdcf219cdf53457e48af3d92684146fdeb90727"
	if got != want {
		t.Fatalf("redisDocID() = %q, want %q", got, want)
	}
	if redisDocID("other input") == got {
		t.Fatal("redisDocID() must differ for different inputs")
	}
}

func TestMergeCacheWriteProtectsHumanVerifiedRows(t *testing.T) {
	humanRow := Entry{
		RawAddress:        "old raw",
		NormalizedAddress: "norm",
		Source:            SourceHumanVerified,
		Structured:        structured.Address{Country: "FR", Town: "PARIS"},
		Embedding:         []float64{0.1},
	}
	pipelineWrite := Entry{
		RawAddress:        "new raw",
		NormalizedAddress: "norm",
		Source:            SourceCRFPipeline,
		Structured:        structured.Address{Country: "DE", Town: "BERLIN"},
		Embedding:         []float64{0.2},
	}

	tests := []struct {
		name          string
		existing      Entry
		existingFound bool
		incoming      Entry
		wantSource    Source
		wantTown      string
		wantRaw       string
		wantEmbedding float64
	}{
		{
			name:          "no existing row keeps incoming",
			existingFound: false,
			incoming:      pipelineWrite,
			wantSource:    SourceCRFPipeline,
			wantTown:      "BERLIN",
			wantRaw:       "new raw",
			wantEmbedding: 0.2,
		},
		{
			name:          "pipeline write cannot downgrade human_verified",
			existing:      humanRow,
			existingFound: true,
			incoming:      pipelineWrite,
			wantSource:    SourceHumanVerified,
			wantTown:      "PARIS",
			wantRaw:       "new raw",
			wantEmbedding: 0.2,
		},
		{
			name:          "human_verified write overwrites human_verified",
			existing:      humanRow,
			existingFound: true,
			incoming: Entry{
				RawAddress:        "new raw",
				NormalizedAddress: "norm",
				Source:            SourceHumanVerified,
				Structured:        structured.Address{Country: "DE", Town: "BERLIN"},
				Embedding:         []float64{0.2},
			},
			wantSource:    SourceHumanVerified,
			wantTown:      "BERLIN",
			wantRaw:       "new raw",
			wantEmbedding: 0.2,
		},
		{
			name: "pipeline write overwrites pipeline row",
			existing: Entry{
				RawAddress:        "old raw",
				NormalizedAddress: "norm",
				Source:            SourceLLMAssisted,
				Structured:        structured.Address{Country: "FR", Town: "PARIS"},
				Embedding:         []float64{0.1},
			},
			existingFound: true,
			incoming:      pipelineWrite,
			wantSource:    SourceCRFPipeline,
			wantTown:      "BERLIN",
			wantRaw:       "new raw",
			wantEmbedding: 0.2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged := mergeCacheWrite(tt.existing, tt.existingFound, tt.incoming)
			if merged.Source != tt.wantSource {
				t.Fatalf("Source = %q, want %q", merged.Source, tt.wantSource)
			}
			if merged.Structured.Town != tt.wantTown {
				t.Fatalf("Structured.Town = %q, want %q", merged.Structured.Town, tt.wantTown)
			}
			if merged.RawAddress != tt.wantRaw {
				t.Fatalf("RawAddress = %q, want %q", merged.RawAddress, tt.wantRaw)
			}
			if len(merged.Embedding) != 1 || merged.Embedding[0] != tt.wantEmbedding {
				t.Fatalf("Embedding = %v, want [%v]", merged.Embedding, tt.wantEmbedding)
			}
		})
	}
}

func TestSemanticScoreFromDistanceMatchesPgvectorSemantics(t *testing.T) {
	if got := semanticScoreFromDistance(0.0); got != 1.0 {
		t.Fatalf("semanticScoreFromDistance(0) = %v, want 1", got)
	}
	if got := semanticScoreFromDistance(0.25); got != 0.75 {
		t.Fatalf("semanticScoreFromDistance(0.25) = %v, want 0.75", got)
	}
}

func TestParseDistanceAcceptsFloatAndString(t *testing.T) {
	if got, err := parseDistance(0.5); err != nil || got != 0.5 {
		t.Fatalf("parseDistance(0.5) = %v, %v; want 0.5, nil", got, err)
	}
	if got, err := parseDistance("0.125"); err != nil || got != 0.125 {
		t.Fatalf("parseDistance(\"0.125\") = %v, %v; want 0.125, nil", got, err)
	}
	if _, err := parseDistance(nil); err == nil {
		t.Fatal("parseDistance(nil) must return an error")
	}
	if _, err := parseDistance("not-a-number"); err == nil {
		t.Fatal("parseDistance(\"not-a-number\") must return an error")
	}
}

func TestEntryFromDocDecodesStoredFields(t *testing.T) {
	doc := map[string]any{
		"raw_address":        "77 RUE DE RIVOLI 75001 PARIS",
		"normalized_address": "77 rue de rivoli 75001 paris",
		"source":             "crf_pipeline",
		"structured":         `{"address_line":"77 RUE DE RIVOLI 75001 PARIS","country":"FR","town":"PARIS"}`,
	}
	entry, err := entryFromDoc(doc)
	if err != nil {
		t.Fatalf("entryFromDoc() error = %v", err)
	}
	if entry.RawAddress != "77 RUE DE RIVOLI 75001 PARIS" {
		t.Fatalf("RawAddress = %q", entry.RawAddress)
	}
	if entry.NormalizedAddress != "77 rue de rivoli 75001 paris" {
		t.Fatalf("NormalizedAddress = %q", entry.NormalizedAddress)
	}
	if entry.Source != SourceCRFPipeline {
		t.Fatalf("Source = %q, want crf_pipeline", entry.Source)
	}
	if entry.Structured.Country != "FR" || entry.Structured.Town != "PARIS" {
		t.Fatalf("Structured = %+v", entry.Structured)
	}
}

func TestEntryFromDocRejectsInvalidStructuredJSON(t *testing.T) {
	if _, err := entryFromDoc(map[string]any{"structured": "{not json"}); err == nil {
		t.Fatal("entryFromDoc() must reject invalid structured JSON")
	}
}

func TestNewRedisStoreRequiresURL(t *testing.T) {
	if _, err := NewRedisStore(context.Background(), "  ", 0); err == nil {
		t.Fatal("NewRedisStore() must reject an empty redis URL")
	}
}

func TestRedisStoreMethodsRejectNilStore(t *testing.T) {
	var s *RedisStore
	ctx := context.Background()

	if _, _, err := s.LookupNormalized(ctx, "x"); err == nil {
		t.Fatal("LookupNormalized on nil store must error")
	}
	if _, err := s.Search(ctx, "x", []float64{0.1}, 5); err == nil {
		t.Fatal("Search on nil store must error")
	}
	if err := s.Upsert(ctx, Entry{}); err == nil {
		t.Fatal("Upsert on nil store must error")
	}
	s.Close() // must not panic
}

func TestRedisStoreValidatesInputsBeforeNetwork(t *testing.T) {
	// A store with a nil index still validates inputs first, proving the
	// same validation contract as PostgresStore without needing a server.
	s := &RedisStore{}
	ctx := context.Background()

	if _, _, err := s.LookupNormalized(ctx, "   "); err == nil {
		t.Fatal("LookupNormalized must require a normalized address")
	}
	if _, err := s.Search(ctx, "x", nil, 5); err == nil {
		t.Fatal("Search must require an embedding")
	}
	if err := s.Upsert(ctx, Entry{Embedding: []float64{0.1}, Source: SourceCRFPipeline}); err == nil {
		t.Fatal("Upsert must require a normalized address")
	}
	if err := s.Upsert(ctx, Entry{NormalizedAddress: "n", Source: SourceCRFPipeline}); err == nil {
		t.Fatal("Upsert must require an embedding")
	}
	if err := s.Upsert(ctx, Entry{NormalizedAddress: "n", Embedding: []float64{0.1}}); err == nil {
		t.Fatal("Upsert must require a source")
	}
}

func TestRedisIndexSchemaBuildsWithDefaultAndCustomDims(t *testing.T) {
	if _, err := redisIndexSchema(0); err != nil {
		t.Fatalf("redisIndexSchema(0) error = %v", err)
	}
	if _, err := redisIndexSchema(1536); err != nil {
		t.Fatalf("redisIndexSchema(1536) error = %v", err)
	}
}
