# LOOK AT ME!

![IATAN](docs/iatan_header.png)

# I AM THE ADMIN NOW!

---

## IATAN (I Am The Admin Now)

IATAN is a self-hosted AI platform that autonomously builds, deploys, and monitors (multiple) websites. One binary.

It plans, designs, writes code, creates databases, sets up APIs, scheduled tasks, webhooks and reviews its own work, and keeps the site healthy, all on its own.
Just point your domain(s) to the server IP and you're good to go, SSL is configured automatically and free (<3 Let's Encrypt / Caddy>).

No Docker. No npm. No database server. No build steps. No webserver to install.
It works out of the box (fingers crossed :).

**IMPORTANT: This is a very early release to get feedback and ideas from the community. Erorrs WILL be there, promised! Have been mainly testing with Z.ai (GLM-5) throughout but Claude should give better results, it was just too costly for me to blow tokens during development. Also deployment on Linux has not been tested, feedback is very much welcomed.**

To see IATAN in action visit [IATAN's home](https://iamtheadminnow.com), the brain creates and maintains this as we speak.

## Features

- **Autonomous site/app building** : Describe what you want, the AI plans, designs, codes, and deploys it
- **Human-in-the-loop** : The brain asks clarifying questions when your description is vague and pauses until you answer
- **Incremental updates** : Tell the brain what to change, only affected pages and components get rebuilt
- **Dynamic REST APIs** : Auto-generated CRUD endpoints with filtering, sorting, pagination, and role-based access control
- **Dynamic database tables** : The AI creates SQLite tables on the fly, with secure column types (PASSWORD via bcrypt, ENCRYPTED via AES-256)
- **User authentication** : JWT-based auth with bcrypt password hashing, configurable roles, and secure token management
- **OAuth / social login** : Google, GitHub, Discord, and any OAuth 2.0 provider, configured by the AI with HMAC-signed state and automatic user provisioning
- **Role-based access control** : Per-endpoint role enforcement, default role assignment on registration, and role-aware API middleware
- **File uploads** : Public upload endpoints with MIME type validation, size limits (10MB), and metadata tracking
- **Real-time updates (SSE + WebSocket)** : SSE for live data feeds (server to client), WebSocket for bidirectional communication (chat, collaboration)
- **Email sending** : Provider-agnostic email with template support (SendGrid, Mailgun, Resend, SES, or any REST API)
- **Payment flows** : Generic checkout integration (Stripe, PayPal, Mollie, Square, or any provider) with webhook signature verification
- **Aggregation queries** : COUNT, SUM, AVG, MIN, MAX with GROUP BY through the public API
- **Webhooks** : Incoming and outgoing, 20+ event types, HMAC-SHA256 signature verification, retry with exponential backoff
- **Scheduled tasks** : Cron-based automation the AI sets up and manages itself
- **Service providers** : Connect any external API with stored credentials and automatic auth injection
- **Encrypted secrets** : AES-256-GCM storage for API keys and sensitive credentials
- **Analytics** : Page view tracking with unique visitors, top pages, referrers, and date range filtering
- **Site memory** : Persistent key-value store the AI uses to remember context across sessions
- **Multi-site/app** : Run unlimited sites from a single instance, each with its own database and isolated storage
- **Free HTTPS** : Embedded Caddy with automatic Let's Encrypt certificates
- **Self-healing monitoring** : Adaptive health checks (5-15 min) that detect and fix issues autonomously
- **Crash recovery** : Pipeline checkpoint system, the brain resumes exactly where it left off after a restart
- **Version history** : Full page and file versioning with rollback support
- **LLM logging** : Full request/response logging with CSV export, token usage stats, and 30-day auto-cleanup
- **Prompt caching** : Anthropic-specific optimization for reduced token usage on repeated prompts

## Simple Setup

![Get Going in No Time!](docs/demo.gif)

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

## Quick Start

```bash
# Linux / macOS
chmod +x ./iatan && ./iatan

# Windows
.\iatan.exe
```

Open `http://localhost:5001` : the setup wizard handles the rest in about 2 minutes.

Your site is served at `http://localhost:5000`.

---

## Download

Grab the latest release:

- **[IATAN_VX.X.X.zip](https://github.com/markdr-hue/IATAN/releases)** : contains `iatan.exe`, `config.json`, and `firstrun.json`

Unzip somewhere convenient, run `iatan.exe`, open `http://localhost:5001`.

---

## The Pipeline

Every site goes through the same deterministic build process:

| Stage | What Happens |
|---|---|
| **PLAN** | Analyzes your description, asks clarifying questions if needed, produces a structured JSON site plan |
| **DESIGN** | Creates CSS theme, layout system, SPA router |
| **BLUEPRINT** | Generates per-page HTML skeletons, component patterns, and content notes for cross-page consistency |
| **DATA LAYER** | Tables, schemas, auth, OAuth, API endpoints with RBAC (skipped if not needed) |
| **BUILD PAGES** | One focused LLM call per page, built sequentially with shared context |
| **REVIEW** | Go-based HTML/CSS/link/heading/metadata validation + LLM fix cycle |
| **COMPLETE** | Notifies the owner, switches to monitoring mode |
| **MONITORING** | Adaptive health checks (5-15 min intervals), self-healing |

Updates use an incremental path (`UPDATE_PLAN`) : only the affected pages and components get rebuilt via a PlanPatch (add, modify, or remove pages; update CSS/nav; add tables).

The brain uses **18 registered tools** with 100+ actions covering pages, files, schemas, data, endpoints, memory, communication, analytics, HTTP, webhooks, providers, secrets, site config, scheduling, layouts, diagnostics, email, and payments.

---

## LLM Providers

IATAN supports two provider types out of the box:

- **Anthropic** (native API) : Claude models with prompt caching support
- **OpenAI-compatible** : Any API that follows the OpenAI chat completions format

The `firstrun.json` file pre-seeds these providers so they appear in the setup wizard:

| Provider | Type | Notes |
|---|---|---|
| Anthropic Claude | anthropic | Sonnet 4.6, Haiku 4.5, Opus 4.6 |
| Ollama (local) | openai-compatible | Free, runs locally, no API key needed |
| Z.ai (GLM) | openai-compatible | GLM-5, GLM-4.7, and Flash variants |

You can add any OpenAI-compatible provider through the setup wizard or by editing `firstrun.json`.

---

## Admin Dashboard

The admin panel at `:5001` is a vanilla JS single-page app (no build step, no Node.js) with a dark-first design.

- **Setup wizard** : Provider selection, admin account, first site creation
- **Site dashboard** : Brain status, controls (start/stop/pause), mode switching
- **Chat** : Streaming conversation with the brain via SSE
- **Pages** : View and edit generated pages with version history
- **Assets & files** : Manage CSS, JS, images, and user uploads
- **Tables** : Browse and edit dynamic database tables created by the AI
- **Endpoints** : View auto-generated REST API and auth routes
- **LLM logs** : Full request/response history with token stats and CSV export
- **Analytics** : Page views, unique visitors, top pages, referrers
- **Diagnostics** : Site health checks and integrity validation
- **Memory, secrets, webhooks, scheduled tasks, layouts, service providers** : All manageable from the UI
- **Questions** : View and answer questions the brain has asked

---

## Production HTTPS

IATAN has [Caddy](https://caddyserver.com) built into the binary. When you're ready to go live:

1. Point your domain's DNS to your server
2. Set the domain on your site in the admin panel
3. Set `IATAN_CADDY_ENABLED=true`
4. Restart

Caddy automatically gets a free SSL certificate from Let's Encrypt. No certbot, no nginx, no renewal cron jobs.

Make sure port 80 and 443 can be reached to validate the domain. See troubleshooting section for more info.

---

## Configuration

IATAN works with zero configuration. If you want to tweak things:

```bash
cp config.example.json config.json
```

Then edit `config.json`:

```json
{
  "admin_port": 5001,
  "public_port": 5000,
  "data_dir": "./data",
  "log_level": "info",
  "caddy_enabled": false,
  "cors_origins": [],
  "rate_limit_rate": 100,
  "rate_limit_burst": 200
}
```

All fields are optional, only include what you want to change. The example file (`config.example.json`) ships with the defaults.

Or use environment variables with the `IATAN_` prefix:

```bash
IATAN_ADMIN_PORT=5001
IATAN_PUBLIC_PORT=5000
IATAN_DATA_DIR=./data
IATAN_LOG_LEVEL=debug
IATAN_CADDY_ENABLED=true
IATAN_FIRSTRUN_PATH=firstrun.json
```

`IATAN_JWT_SECRET` and `IATAN_ENCRYPTION_KEY` are auto-generated on first run if not set.

LLM API keys are configured through the setup wizard, but you can also set them as environment variables:

```bash
ANTHROPIC_API_KEY=sk-ant-...
ZAI_API_KEY=...
```

Any OpenAI-compatible provider can be added through the setup wizard or `firstrun.json`.

Priority: environment variables > `config.json` > defaults.

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

## Architecture

```
                    You
                     |
              localhost:5001 (admin)
                     |
            +--------+--------+
            |    IATAN Core   |
            |                 |
            |  Pipeline Brain |
            |  Chat (SSE)     |
            |  18 Tools       |
            |  Event Bus      |
            |  Per-site SQLite|
            +--------+--------+
                     |
              localhost:5000 (public)
                     |
                  Visitors
```

- **Admin** (`:5001`) : Manage sites, chat with the brain, view logs and analytics. Vanilla JS SPA embedded in the binary.
- **Public** (`:5000`) : Your generated sites served to visitors, with dynamic API endpoints, file uploads, SSE streams, WebSocket connections, and webhook ingestion.
- **Brain** : One goroutine per site (max 4 concurrent), channel-based commands, deterministic pipeline with crash recovery via `pipeline_state` checkpoint.
- **Database** : Main DB (`data/iatan.db`) for users, providers, sites + per-site SQLite DB (`data/sites/{id}/site.db`) for pages, tables, endpoints, and everything else. Pure Go SQLite, no CGO.
- **Chat** : Streaming conversation via SSE, separate from the build pipeline. The brain can use all 18 tools during chat to make changes on demand.
- **Event bus** : 22 event types drive real-time updates, webhook delivery, and inter-component coordination.
- **Real-time** : SSE (`/api/{path}/stream`) for server-to-client feeds, WebSocket (`/api/{path}/ws`) for bidirectional communication. The AI brain decides which to create based on the site's needs.
- **Caddy** : Optional embedded reverse proxy with automatic HTTPS via Let's Encrypt.

---

## Requirements

- An LLM API key (Anthropic, Z.ai, OpenAI, or any OpenAI-compatible provider) : or [Ollama](https://ollama.com) for fully local/free operations
- That's it

---

## Security

### Lock down the admin port

The admin panel runs on port **5001** by default. If your server's firewall allows it, **anyone on the internet can reach it**. You should block external access and use an SSH tunnel instead:

**Windows Server:**

```powershell
netsh advfirewall firewall add rule name="Block IATAN Admin" dir=in action=block protocol=tcp localport=5001
```

**Linux (ufw):**

```bash
sudo ufw deny 5001/tcp
```

Then access admin remotely through an SSH tunnel:

```bash
ssh -L 5001:localhost:5001 user@yourserver
```

Open `http://localhost:5001` on your local machine : the traffic is encrypted through SSH and the port stays closed to the outside world.

---

## Troubleshooting

### HTTPS / Let's Encrypt fails with "Timeout during connect"

Caddy needs ports **80** and **443** open for Let's Encrypt to verify your domain. Open them on your server's firewall:

**Windows Server:**

```powershell
netsh advfirewall firewall add rule name="HTTP" dir=in action=allow protocol=tcp localport=80
netsh advfirewall firewall add rule name="HTTPS" dir=in action=allow protocol=tcp localport=443
```

**Linux (ufw):**

```bash
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
```

Also check your hosting provider's control panel : many VPS providers have a separate firewall/security group that needs ports 80 and 443 allowed.

Once the ports are open, restart IATAN and Caddy will automatically retry and get the certificate.

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

- Full-time single dad of 3, with a full-time job to match.
- My ideas and perspective tend to be a bit unconventional, which means I'm often misunderstood or out of step with the people around me.
- I frequently question whether I see the world differently from everyone else. I've made peace with the fact that the answer is probably yes.

---

## License

MIT
