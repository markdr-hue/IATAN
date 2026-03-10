/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package tools

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// PaymentsTool — manage_payments
// ---------------------------------------------------------------------------

// PaymentsTool provides generic payment flow management that works with any
// payment provider (Stripe, PayPal, Mollie, Square, etc.) via service_providers.
type PaymentsTool struct{}

func (t *PaymentsTool) Name() string { return "manage_payments" }
func (t *PaymentsTool) Description() string {
	return "Manage payment flows. Actions: configure (set up payment provider), create_checkout (create checkout session), " +
		"check_status (check payment status), list (list payments), handle_webhook (process payment webhooks). " +
		"Works with any payment provider (Stripe, PayPal, Mollie, Square, etc.) via service_providers."
}

func (t *PaymentsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"configure", "create_checkout", "check_status", "list", "handle_webhook"},
				"description": "Action to perform",
			},
			"provider_name": map[string]interface{}{
				"type":        "string",
				"description": "Name of the service provider (must exist in manage_providers). For configure.",
			},
			"provider_type": map[string]interface{}{
				"type":        "string",
				"description": "Provider type hint: stripe, paypal, mollie, square, generic. For configure.",
				"enum":        []string{"stripe", "paypal", "mollie", "square", "generic"},
			},
			"currency": map[string]interface{}{
				"type":        "string",
				"description": "Default currency code (e.g. 'usd', 'eur'). For configure.",
			},
			"webhook_secret_name": map[string]interface{}{
				"type":        "string",
				"description": "Name of secret storing the webhook signing key. For configure.",
			},
			"amount": map[string]interface{}{
				"type":        "number",
				"description": "Amount in cents (e.g. 1999 = $19.99). For create_checkout.",
			},
			"description": map[string]interface{}{
				"type":        "string",
				"description": "Payment description. For create_checkout.",
			},
			"success_url": map[string]interface{}{
				"type":        "string",
				"description": "URL to redirect after successful payment. For create_checkout.",
			},
			"cancel_url": map[string]interface{}{
				"type":        "string",
				"description": "URL to redirect on payment cancellation. For create_checkout.",
			},
			"metadata": map[string]interface{}{
				"type":        "object",
				"description": "Custom metadata to attach to the payment. For create_checkout.",
			},
			"payment_id": map[string]interface{}{
				"type":        "number",
				"description": "Local payment ID. For check_status.",
			},
			"external_id": map[string]interface{}{
				"type":        "string",
				"description": "Provider's payment/session ID. For check_status.",
			},
			"webhook_body": map[string]interface{}{
				"type":        "string",
				"description": "Raw webhook body (JSON string). For handle_webhook.",
			},
			"webhook_signature": map[string]interface{}{
				"type":        "string",
				"description": "Webhook signature header value. For handle_webhook.",
			},
		},
		"required": []string{"action"},
	}
}

func (t *PaymentsTool) Execute(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	return DispatchAction(ctx, args, map[string]ActionHandler{
		"configure":       t.configure,
		"create_checkout": t.createCheckout,
		"check_status":    t.checkStatus,
		"list":            t.list,
		"handle_webhook":  t.handleWebhook,
	}, nil)
}

func (t *PaymentsTool) ensureTables(db *sql.DB) {
	db.Exec(`CREATE TABLE IF NOT EXISTS payment_config (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		provider_name TEXT NOT NULL,
		provider_type TEXT NOT NULL DEFAULT 'generic',
		currency TEXT NOT NULL DEFAULT 'usd',
		webhook_secret_name TEXT DEFAULT '',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS payments (
		id INTEGER PRIMARY KEY,
		external_id TEXT DEFAULT '',
		amount INTEGER NOT NULL,
		currency TEXT NOT NULL DEFAULT 'usd',
		status TEXT NOT NULL DEFAULT 'pending',
		description TEXT DEFAULT '',
		metadata TEXT DEFAULT '{}',
		checkout_url TEXT DEFAULT '',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)
}

func (t *PaymentsTool) configure(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	providerName, _ := args["provider_name"].(string)
	providerType, _ := args["provider_type"].(string)
	currency, _ := args["currency"].(string)
	webhookSecretName, _ := args["webhook_secret_name"].(string)

	if providerName == "" {
		return &Result{Success: false, Error: "provider_name is required"}, nil
	}
	if providerType == "" {
		providerType = "generic"
	}
	if currency == "" {
		currency = "usd"
	}

	// Verify the provider exists.
	var exists int
	err := ctx.DB.QueryRow("SELECT COUNT(*) FROM service_providers WHERE name = ?", providerName).Scan(&exists)
	if err != nil || exists == 0 {
		return &Result{Success: false, Error: fmt.Sprintf("provider '%s' not found — create it first with manage_providers", providerName)}, nil
	}

	t.ensureTables(ctx.DB)

	_, err = ctx.DB.Exec(
		`INSERT INTO payment_config (id, provider_name, provider_type, currency, webhook_secret_name)
		 VALUES (1, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   provider_name = excluded.provider_name,
		   provider_type = excluded.provider_type,
		   currency = excluded.currency,
		   webhook_secret_name = excluded.webhook_secret_name`,
		providerName, providerType, currency, webhookSecretName,
	)
	if err != nil {
		return nil, fmt.Errorf("configuring payments: %w", err)
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"provider": providerName,
		"type":     providerType,
		"currency": currency,
	}}, nil
}

func (t *PaymentsTool) createCheckout(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	amountFloat, _ := args["amount"].(float64)
	amount := int(amountFloat)
	description, _ := args["description"].(string)
	successURL, _ := args["success_url"].(string)
	cancelURL, _ := args["cancel_url"].(string)
	metadata, _ := args["metadata"].(map[string]interface{})

	if amount <= 0 {
		return &Result{Success: false, Error: "amount (in cents) is required and must be positive"}, nil
	}
	if successURL == "" || cancelURL == "" {
		return &Result{Success: false, Error: "success_url and cancel_url are required"}, nil
	}

	// Load payment config.
	t.ensureTables(ctx.DB)
	var providerName, providerType, currency string
	err := ctx.DB.QueryRow(
		"SELECT provider_name, provider_type, currency FROM payment_config WHERE id = 1",
	).Scan(&providerName, &providerType, &currency)
	if err != nil {
		return &Result{Success: false, Error: "payments not configured — use manage_payments(action='configure') first"}, nil
	}

	// Override currency if specified.
	if cur, ok := args["currency"].(string); ok && cur != "" {
		currency = cur
	}

	metadataJSON := "{}"
	if metadata != nil {
		data, _ := json.Marshal(metadata)
		metadataJSON = string(data)
	}

	// Load provider details.
	var baseURL, authType, authHeader, authPrefix string
	var secretName sql.NullString
	err = ctx.DB.QueryRow(
		`SELECT base_url, auth_type, auth_header, auth_prefix, secret_name
		 FROM service_providers WHERE name = ? AND is_enabled = 1`,
		providerName,
	).Scan(&baseURL, &authType, &authHeader, &authPrefix, &secretName)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("payment provider '%s' not found or disabled", providerName)}, nil
	}

	// Build request based on provider type.
	var reqURL, reqBody, contentType string
	switch providerType {
	case "stripe":
		reqURL = strings.TrimRight(baseURL, "/") + "/v1/checkout/sessions"
		contentType = "application/x-www-form-urlencoded"
		reqBody = fmt.Sprintf("mode=payment&success_url=%s&cancel_url=%s&line_items[0][price_data][currency]=%s&line_items[0][price_data][unit_amount]=%d&line_items[0][price_data][product_data][name]=%s&line_items[0][quantity]=1",
			successURL, cancelURL, currency, amount, description)

	case "paypal":
		reqURL = strings.TrimRight(baseURL, "/") + "/v2/checkout/orders"
		contentType = "application/json"
		amountStr := fmt.Sprintf("%.2f", float64(amount)/100.0)
		payload := map[string]interface{}{
			"intent": "CAPTURE",
			"purchase_units": []map[string]interface{}{
				{
					"amount": map[string]string{
						"currency_code": strings.ToUpper(currency),
						"value":         amountStr,
					},
					"description": description,
				},
			},
			"application_context": map[string]string{
				"return_url": successURL,
				"cancel_url": cancelURL,
			},
		}
		data, _ := json.Marshal(payload)
		reqBody = string(data)

	case "mollie":
		reqURL = strings.TrimRight(baseURL, "/") + "/v2/payments"
		contentType = "application/json"
		amountStr := fmt.Sprintf("%.2f", float64(amount)/100.0)
		payload := map[string]interface{}{
			"amount": map[string]string{
				"currency": strings.ToUpper(currency),
				"value":    amountStr,
			},
			"description": description,
			"redirectUrl": successURL,
			"cancelUrl":   cancelURL,
		}
		data, _ := json.Marshal(payload)
		reqBody = string(data)

	default: // "square", "generic"
		reqURL = strings.TrimRight(baseURL, "/") + "/payments"
		contentType = "application/json"
		payload := map[string]interface{}{
			"amount":      amount,
			"currency":    currency,
			"description": description,
			"success_url": successURL,
			"cancel_url":  cancelURL,
			"metadata":    metadata,
		}
		data, _ := json.Marshal(payload)
		reqBody = string(data)
	}

	// Create HTTP request.
	req, err := http.NewRequest("POST", reqURL, strings.NewReader(reqBody))
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("creating request: %v", err)}, nil
	}
	req.Header.Set("Content-Type", contentType)

	// Inject auth.
	if authType != "none" && secretName.Valid && secretName.String != "" {
		var encryptedValue string
		err := ctx.DB.QueryRow("SELECT value_encrypted FROM secrets WHERE name = ?", secretName.String).Scan(&encryptedValue)
		if err != nil {
			return &Result{Success: false, Error: fmt.Sprintf("secret '%s' not found", secretName.String)}, nil
		}
		if ctx.Encryptor == nil {
			return &Result{Success: false, Error: "encryption not available"}, nil
		}
		secretValue, err := ctx.Encryptor.Decrypt(encryptedValue)
		if err != nil {
			return &Result{Success: false, Error: "failed to decrypt secret"}, nil
		}
		switch authType {
		case "bearer":
			req.Header.Set(authHeader, authPrefix+" "+secretValue)
		case "api_key_header":
			req.Header.Set(authHeader, secretValue)
		case "basic":
			req.Header.Set(authHeader, "Basic "+secretValue)
		}
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("creating checkout: %v", err)}, nil
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 16384))

	if resp.StatusCode >= 400 {
		return &Result{Success: false, Error: fmt.Sprintf("provider returned %d: %s", resp.StatusCode, string(respBody))}, nil
	}

	// Parse response to extract checkout URL and external ID.
	var respData map[string]interface{}
	json.Unmarshal(respBody, &respData)

	checkoutURL := ""
	externalID := ""
	switch providerType {
	case "stripe":
		checkoutURL, _ = respData["url"].(string)
		externalID, _ = respData["id"].(string)
	case "paypal":
		externalID, _ = respData["id"].(string)
		if links, ok := respData["links"].([]interface{}); ok {
			for _, l := range links {
				if link, ok := l.(map[string]interface{}); ok {
					if rel, _ := link["rel"].(string); rel == "approve" {
						checkoutURL, _ = link["href"].(string)
					}
				}
			}
		}
	case "mollie":
		externalID, _ = respData["id"].(string)
		if links, ok := respData["_links"].(map[string]interface{}); ok {
			if checkout, ok := links["checkout"].(map[string]interface{}); ok {
				checkoutURL, _ = checkout["href"].(string)
			}
		}
	default:
		checkoutURL, _ = respData["checkout_url"].(string)
		if checkoutURL == "" {
			checkoutURL, _ = respData["url"].(string)
		}
		externalID, _ = respData["id"].(string)
		if externalID == "" {
			if id, ok := respData["payment_id"].(string); ok {
				externalID = id
			}
		}
	}

	// Store payment locally.
	result, err := ctx.DB.Exec(
		`INSERT INTO payments (external_id, amount, currency, status, description, metadata, checkout_url)
		 VALUES (?, ?, ?, 'pending', ?, ?, ?)`,
		externalID, amount, currency, description, metadataJSON, checkoutURL,
	)
	if err != nil {
		return nil, fmt.Errorf("storing payment: %w", err)
	}

	paymentID, _ := result.LastInsertId()

	return &Result{Success: true, Data: map[string]interface{}{
		"payment_id":   paymentID,
		"external_id":  externalID,
		"checkout_url": checkoutURL,
		"amount":       amount,
		"currency":     currency,
	}}, nil
}

func (t *PaymentsTool) checkStatus(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	t.ensureTables(ctx.DB)

	var status, externalID, currency, description string
	var amount int
	var paymentID int64

	if pid, ok := args["payment_id"].(float64); ok {
		paymentID = int64(pid)
		err := ctx.DB.QueryRow(
			"SELECT status, external_id, amount, currency, description FROM payments WHERE id = ?",
			paymentID,
		).Scan(&status, &externalID, &amount, &currency, &description)
		if err != nil {
			return &Result{Success: false, Error: "payment not found"}, nil
		}
	} else if eid, ok := args["external_id"].(string); ok && eid != "" {
		externalID = eid
		err := ctx.DB.QueryRow(
			"SELECT id, status, amount, currency, description FROM payments WHERE external_id = ?",
			externalID,
		).Scan(&paymentID, &status, &amount, &currency, &description)
		if err != nil {
			return &Result{Success: false, Error: "payment not found"}, nil
		}
	} else {
		return &Result{Success: false, Error: "payment_id or external_id is required"}, nil
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"payment_id":  paymentID,
		"external_id": externalID,
		"status":      status,
		"amount":      amount,
		"currency":    currency,
		"description": description,
	}}, nil
}

func (t *PaymentsTool) list(ctx *ToolContext, _ map[string]interface{}) (*Result, error) {
	t.ensureTables(ctx.DB)

	rows, err := ctx.DB.Query(
		"SELECT id, external_id, amount, currency, status, description, created_at FROM payments ORDER BY created_at DESC LIMIT 50",
	)
	if err != nil {
		return nil, fmt.Errorf("listing payments: %w", err)
	}
	defer rows.Close()

	var payments []map[string]interface{}
	for rows.Next() {
		var id, amount int
		var externalID, currency, status, description string
		var createdAt time.Time
		if err := rows.Scan(&id, &externalID, &amount, &currency, &status, &description, &createdAt); err != nil {
			continue
		}
		payments = append(payments, map[string]interface{}{
			"id":          id,
			"external_id": externalID,
			"amount":      amount,
			"currency":    currency,
			"status":      status,
			"description": description,
			"created_at":  createdAt,
		})
	}

	return &Result{Success: true, Data: payments}, nil
}

func (t *PaymentsTool) handleWebhook(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	t.ensureTables(ctx.DB)

	body, _ := args["webhook_body"].(string)
	signature, _ := args["webhook_signature"].(string)
	if body == "" {
		return &Result{Success: false, Error: "webhook_body is required"}, nil
	}

	// Load payment config for webhook verification.
	var providerType, webhookSecretName string
	err := ctx.DB.QueryRow(
		"SELECT provider_type, webhook_secret_name FROM payment_config WHERE id = 1",
	).Scan(&providerType, &webhookSecretName)
	if err != nil {
		return &Result{Success: false, Error: "payments not configured"}, nil
	}

	// Verify webhook signature if secret is configured.
	if webhookSecretName != "" && signature != "" && ctx.Encryptor != nil {
		var encryptedValue string
		err := ctx.DB.QueryRow("SELECT value_encrypted FROM secrets WHERE name = ?", webhookSecretName).Scan(&encryptedValue)
		if err == nil {
			secretValue, decErr := ctx.Encryptor.Decrypt(encryptedValue)
			if decErr == nil {
				if !verifyWebhookSignature(providerType, body, signature, secretValue) {
					return &Result{Success: false, Error: "webhook signature verification failed"}, nil
				}
			}
		}
	}

	// Parse webhook body.
	var webhookData map[string]interface{}
	if err := json.Unmarshal([]byte(body), &webhookData); err != nil {
		return &Result{Success: false, Error: "invalid webhook JSON"}, nil
	}

	// Extract payment ID and status based on provider type.
	var externalID, newStatus string
	switch providerType {
	case "stripe":
		if obj, ok := webhookData["data"].(map[string]interface{}); ok {
			if inner, ok := obj["object"].(map[string]interface{}); ok {
				externalID, _ = inner["id"].(string)
				if paymentStatus, ok := inner["payment_status"].(string); ok {
					if paymentStatus == "paid" {
						newStatus = "paid"
					} else {
						newStatus = paymentStatus
					}
				}
			}
		}
	case "paypal":
		if resource, ok := webhookData["resource"].(map[string]interface{}); ok {
			externalID, _ = resource["id"].(string)
			if status, ok := resource["status"].(string); ok {
				if status == "COMPLETED" {
					newStatus = "paid"
				} else {
					newStatus = strings.ToLower(status)
				}
			}
		}
	case "mollie":
		externalID, _ = webhookData["id"].(string)
		if status, ok := webhookData["status"].(string); ok {
			if status == "paid" {
				newStatus = "paid"
			} else {
				newStatus = status
			}
		}
	default:
		externalID, _ = webhookData["id"].(string)
		if externalID == "" {
			externalID, _ = webhookData["payment_id"].(string)
		}
		newStatus, _ = webhookData["status"].(string)
	}

	if externalID == "" || newStatus == "" {
		return &Result{Success: false, Error: "could not extract payment ID or status from webhook"}, nil
	}

	// Update payment status.
	_, err = ctx.DB.Exec(
		"UPDATE payments SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE external_id = ?",
		newStatus, externalID,
	)
	if err != nil {
		return nil, fmt.Errorf("updating payment status: %w", err)
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"external_id": externalID,
		"status":      newStatus,
	}}, nil
}

// verifyWebhookSignature performs HMAC-SHA256 verification for webhook signatures.
func verifyWebhookSignature(providerType, body, signature, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	// Different providers format signatures differently.
	switch providerType {
	case "stripe":
		// Stripe uses "t=timestamp,v1=signature" format — extract v1.
		for _, part := range strings.Split(signature, ",") {
			if strings.HasPrefix(part, "v1=") {
				return hmac.Equal([]byte(part[3:]), []byte(expectedMAC))
			}
		}
		return false
	default:
		// Generic: compare directly.
		return hmac.Equal([]byte(signature), []byte(expectedMAC))
	}
}

func (t *PaymentsTool) Summarize(result string) string {
	r, dataMap, dataArr, ok := parseSummaryResult(result)
	if !ok {
		return summarizeTruncate(result, 200)
	}
	if !r.Success {
		return summarizeError(r.Error)
	}
	if dataArr != nil {
		return fmt.Sprintf(`{"success":true,"summary":"Listed %d payments"}`, len(dataArr))
	}
	if status, _ := dataMap["status"].(string); status != "" {
		return fmt.Sprintf(`{"success":true,"summary":"Payment status: %s"}`, status)
	}
	return summarizeTruncate(result, 300)
}
