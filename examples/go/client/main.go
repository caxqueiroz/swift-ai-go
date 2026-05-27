package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

type convertRequest struct {
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
	Country    string `json:"country"`
	Town       string `json:"town"`
	PostalCode string `json:"postal_code"`
	Street     string `json:"street"`
}

func main() {
	body, err := json.Marshal(convertRequest{Text: "77 RUE DE RIVOLI 75001 PARIS"})
	if err != nil {
		log.Fatal(err)
	}
	resp, err := http.Post("http://localhost:8080/convert", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("convert failed with status %d", resp.StatusCode)
	}

	var payload convertResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		log.Fatal(err)
	}
	for _, item := range payload.Items {
		fmt.Printf("%s -> country=%s town=%s served_by=%s status=%s band=%s\n",
			item.Input, item.Structured.Country, item.Structured.Town, item.ServedBy, item.ResolutionStatus, item.ConfidenceBand)
	}
}
