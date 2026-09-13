package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	receipt "example.com/receipt-order-service"
)

func main() {
	client := receipt.EmailClient{APIKey: os.Getenv("INFRAI_API_KEY"), MaxRetries: 3}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /order-updates", func(w http.ResponseWriter, r *http.Request) {
		var update receipt.OrderUpdate
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		email, shouldSend, err := receipt.BuildEmail(update)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if !shouldSend {
			json.NewEncoder(w).Encode(map[string]any{"email_sent": false, "status": update.Status})
			return
		}
		result, err := client.Send(r.Context(), email, receipt.IdempotencyKey(update))
		if err != nil {
			http.Error(w, "email delivery failed", http.StatusBadGateway)
			log.Printf("order %s: %v", update.OrderID, err)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"email_sent": true, "message_id": result.MessageID})
	})

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("receipt service listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
