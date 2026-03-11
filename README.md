# LOOK AT ME!

![IATAN](docs/iatan_header.png)

# I AM THE ADMIN NOW!

---

## What is IATAN?

**One binary. Describe your site. Done.**

IATAN is a self-hosted AI platform that autonomously builds, deploys, and monitors websites.
It plans, designs, writes code, creates databases, sets up APIs and auth, and keeps everything running...all on its own.

No Docker. No npm. No database server. No webserver. No build steps. Just run it.

**IMPORTANT: This is an early release to get feedback and ideas from the community. Errors WILL be there, promised! Have been mainly testing with Z.ai (GLM-5) throughout but Claude should give better results, it was just too costly for me to blow tokens during development. Also deployment on Linux has not been tested, feedback is very much welcomed.**

To see IATAN in action visit [IATAN's home](https://iamtheadminnow.com), the brain creates and maintains this as we speak.

---

## Quick Start

Download from **[Releases](https://github.com/markdr-hue/IATAN/releases)**, run the binary, open `http://localhost:5001`.
The setup wizard handles the rest in ~2 minutes. Your site is served at `http://localhost:5000`.

```bash
# Linux / macOS
chmod +x ./iatan && ./iatan

# Windows
.\iatan.exe
```

---

## Testimonials

> "I showed my grandma IATAN. She asked me what a website is. I said 'just click.' She now has 86 websites and a newsletter with 200,000 subscribers. She thinks she's emailing her friends."
> : Marcus, grandson

> "My cat walked across my keyboard and clicked IATAN. He now has 12 websites, a SaaS platform, and investor meetings on Tuesday."
> : Lisa, cat owner / now cat employee

> "My husband spent 2 years building his startup. I clicked IATAN once and built a better version in front of him. We are now divorced. I got the website."
> : Angela, winner

> "I used IATAN for my daughter's lemonade stand website. She now has a multinational beverage distribution platform across 40 countries. She's 9. I need a lawyer."
> : Sandra, lemonade mom

---

## Core Features

- **Autonomous site building** : Describe what you want, the AI plans, designs, codes, and deploys it
- **Human-in-the-loop** : The brain asks clarifying questions when your description is vague and pauses until you answer
- **Incremental updates** : Tell the brain what to change, only affected pages and components get rebuilt
- **Dynamic databases** : The AI creates SQLite tables on the fly, with secure column types (PASSWORD via bcrypt, ENCRYPTED via AES-256)
- **REST APIs** : Auto-generated CRUD endpoints with filtering, sorting, pagination, and role-based access control
- **User auth + OAuth** : JWT-based auth with bcrypt, configurable roles, plus Google/GitHub/Discord/any OAuth 2.0 provider
- **Free HTTPS** : Embedded Caddy with automatic Let's Encrypt certificates, zero config
- **Multi-site** : Unlimited sites from a single instance, each with its own database and isolated storage

## Advanced Features

- File uploads with MIME validation and size limits
- Real-time updates (SSE for server-to-client, WebSocket for bidirectional)
- Email sending (SendGrid, Mailgun, Resend, SES, or any REST provider)
- Payment flows (Stripe, PayPal, Mollie, Square, or any provider)
- Aggregation queries (COUNT, SUM, AVG, MIN, MAX with GROUP BY)
- Webhooks (HMAC-SHA256 signatures, retry with backoff)
- Scheduled tasks (cron-based, AI-managed)
- Service providers (connect any external API with stored credentials)
- Encrypted secrets (AES-256-GCM for API keys and credentials)
- Analytics (page views, unique visitors, top pages, referrers)
- Site memory (persistent key-value store for cross-session context)
- Self-healing monitoring (adaptive 5-15 min health checks)
- Crash recovery (pipeline checkpoint, resumes exactly where it left off)
- Version history (pages and files with rollback)
- LLM logging with token stats, CSV export, and prompt caching

---

## The Pipeline

Every site goes through the same deterministic build process:

| Stage | What Happens |
|---|---|
| **ANALYZE** | Analyzes your description, asks clarifying questions if needed, produces a structured Analysis JSON |
| **BLUEPRINT** | Creates detailed build specification (pages, endpoints, tables, design) |
| **BUILD** | Single-phase: creates all data tables, endpoints, CSS, layout, and pages |
| **VALIDATE** | Blueprint conformance check — verifies all planned items were created |
| **COMPLETE** | Notifies the owner, switches to monitoring mode |
| **MONITORING** | Adaptive health checks (5-15 min), self-healing |

Updates use an incremental path: only affected pages and components get rebuilt via a BlueprintPatch.

---

## LLM Providers

| Provider | Type | Notes |
|---|---|---|
| Anthropic Claude | anthropic | Sonnet 4.6, Haiku 4.5, Opus 4.6 — with prompt caching |
| Ollama (local) | openai-compatible | Free, runs locally, no API key needed |
| Z.ai (GLM) | openai-compatible | GLM-5, GLM-4.7, and Flash variants |

Any OpenAI-compatible provider can be added through the setup wizard or `firstrun.json`.

---

## Configuration

IATAN works with zero configuration out of the box. Optionally use `config.json` or environment variables with the `IATAN_` prefix to customize ports, data directory, log level, and CORS settings. JWT secrets and encryption keys are auto-generated on first run.

LLM API keys are configured through the setup wizard, or set as environment variables (`ANTHROPIC_API_KEY`, etc.).

---

## Production HTTPS

Point your domain's DNS to your server, set the domain in the admin panel, set `IATAN_CADDY_ENABLED=true`, and restart. Caddy automatically gets a free SSL certificate from Let's Encrypt. Make sure ports 80 and 443 are open.

---

## Build From Source

```bash
# Requires Go 1.25+
git clone https://github.com/markdr-hue/IATAN.git
cd iatan-go

make build          # Build for your platform
make build-linux    # Linux AMD64 + ARM64
make build-darwin   # macOS Intel + Apple Silicon
make build-windows  # Windows AMD64
make build-all      # Cross-compile everything
make dev            # Run in development mode
```

---

## Security

The admin panel runs on port **5001**. If your server is publicly accessible, block this port externally and use an SSH tunnel (`ssh -L 5001:localhost:5001 user@yourserver`) to access it securely.

---

## Warning

- This is for **testing and experimentation**. Not production. Probably.
- There will be errors, trust me
- It autonomously generates websites using AI, which actively contributes to the Dead Internet Theory. You've been warned.
- Anything it builds may be hilariously wrong, subtly broken, or accidentally brilliant.
- You are solely responsible for any content generated by IATAN.
- IATAN is not responsible for unemployment

---

## Community

- **Discord** : [Join the server](https://discord.gg/VRdYgDQ2qr)
- **X / Twitter** : [@GO_IATAN](https://x.com/GO_IATAN)

---

## About Me

- Full-time single dad of 3, they give me purpose.
- My ideas and perspective tend to be a bit unconventional, which means I'm often misunderstood or out of step with the people around me.
- I frequently question whether I see the world differently from everyone else. I've made peace with the fact that the answer is probably yes.

---

## License

MIT
