package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type convertRequest struct {
	Items []convertRequestItem `json:"items"`
}

type convertRequestItem struct {
	Text string `json:"text"`
}

type convertResponse struct {
	Items []convertItem `json:"items"`
}

type convertItem struct {
	Input            string            `json:"input"`
	Structured       structuredAddress `json:"structured"`
	ServedBy         string            `json:"served_by"`
	ResolutionStatus string            `json:"resolution_status"`
	ConfidenceBand   string            `json:"confidence_band"`
}

type structuredAddress struct {
	Country string `json:"country"`
	Town    string `json:"town"`
}

func convertBatch(ctx context.Context, client *http.Client, apiURL string, records []addressRecord, inputMode string) ([]evaluationRow, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if len(records) == 0 {
		return nil, nil
	}
	request := convertRequest{Items: make([]convertRequestItem, len(records))}
	for i, record := range records {
		request.Items[i] = convertRequestItem{Text: buildInputText(record, inputMode)}
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode convert request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create convert request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("post convert request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("convert returned status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}

	var payload convertResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode convert response: %w", err)
	}
	if len(payload.Items) != len(records) {
		return nil, fmt.Errorf("convert returned %d items, want %d", len(payload.Items), len(records))
	}

	rows := make([]evaluationRow, len(records))
	for i, item := range payload.Items {
		record := records[i]
		rows[i] = evaluationRow{
			ID:              record.ID,
			Address:         buildInputText(record, inputMode),
			Stratum:         record.Stratum,
			ExpectedCountry: record.Country,
			ExpectedTown:    record.Town,
			ActualCountry:   item.Structured.Country,
			ActualTown:      item.Structured.Town,
			ServedBy:        item.ServedBy,
			Status:          item.ResolutionStatus,
			Band:            item.ConfidenceBand,
		}
	}
	return rows, nil
}
