/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package tools

// RegisterAll registers every tool with the given registry.
func RegisterAll(r *Registry) {
	// Pages (unified: save, get, list, delete, restore, history, search)
	r.Register(&PagesTool{})

	// Storage: unified assets + files (storage="assets"|"files")
	r.Register(&FilesTool{})

	// Dynamic tables (with secure columns: PASSWORD, ENCRYPTED)
	r.Register(&SchemaTool{})
	r.Register(&DataTool{})

	// Endpoints (unified: create_api, list_api, delete_api, create_auth, list_auth, delete_auth, verify_password)
	r.Register(&EndpointsTool{})

	// Memory (unified: remember, recall, list, forget)
	r.Register(&MemoryTool{})

	// Communication (unified: ask, check)
	r.Register(&CommunicationTool{})

	// Analytics (unified: query, summary)
	r.Register(&AnalyticsTool{})

	// HTTP
	r.Register(&MakeHTTPRequestTool{})

	// Webhooks (unified: create, get, list, delete, update, subscribe)
	r.Register(&WebhooksTool{})

	// Service Providers (unified: add, list, remove, update, request)
	r.Register(&ProvidersTool{})

	// Secrets (unified: store, list, delete)
	r.Register(&SecretsTool{})

	// Site (unified: info, set_mode)
	r.Register(&SiteTool{})

	// Scheduler (unified: create, list, update, delete)
	r.Register(&SchedulerTool{})

	// Layout (unified: save, get, list)
	r.Register(&LayoutTool{})

	// Diagnostics (unified: health, errors, integrity)
	r.Register(&DiagnosticsTool{})

	// Email (unified: configure, send, save_template, list_templates)
	r.Register(&EmailTool{})

	// Payments (unified: configure, create_checkout, check_status, list, handle_webhook)
	r.Register(&PaymentsTool{})
}
