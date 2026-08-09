# Taking atlas to production: a step-by-step guide

atlas currently runs as a local demo: one Postgres database, a small 3-partition Kafka cluster, a dev-only LLM runner, and resume files stored on a shared disk folder. There is no login of any kind, no way to tell customers' data apart, and no automated testing pipeline. This document lays out, in order, what needs to change before real customers can use this in production at scale.

The plan assumes:
- The service will run across multiple machines (Kubernetes or ECS), not one Docker host.
- LLM and embedding calls will go to a hosted API instead of running our own model server.
- The service will serve multiple customers ("tenants"), and one customer must never see another's data.
- Login will be handled by plugging into an existing identity provider (OIDC/SSO), not built from scratch.
- Both search traffic and resume-upload traffic need to handle much higher volume than today.

Follow the steps below in order. Each one explains what's wrong today, what to do about it, and how to confirm it actually worked. Later steps depend on earlier ones, so don't skip ahead.

## What's already solid (no changes needed)

A few things are already built the right way and don't need rework:
- The service shuts down cleanly — it waits up to 10 seconds for in-flight requests to finish before exiting (`cmd/app/serve.go`).
- Database migrations are safe to run from multiple machines at the same time — they take a lock so two machines starting up at once won't collide (`internal/adapter/driven/postgres/migrate.go`).
- The background job that retries failed resumes is also safe to run on multiple machines at once, for the same reason.
- If a resume fails to process because the database is briefly unreachable, that failure is still recorded reliably once the database comes back (`internal/service/status_write.go`).
- The existing tests run against a real Postgres and Kafka (not fakes), so they actually catch real bugs.

## Step 1: Set up a CI pipeline

**What's wrong today:** There is no automated pipeline at all — no `.github/workflows` or equivalent. Every change to the code is only checked by whoever remembers to run the tests by hand.

**What to do:** Add a CI pipeline that runs on every code change: build the project, run the unit tests (`make test-unit`), and run the integration tests (`make test-integration`, which spins up real Postgres and Kafka containers). Block merging if any of these fail.

**How you know it's done:** Push a change that breaks a test and confirm the pipeline stops it from merging. Push a good change and confirm it merges cleanly.

## Step 2: Make the app able to start up outside of Docker Compose

**What's wrong today:** The app won't even start outside of the current local setup. It needs four settings — the address and model name for the LLM, and the address and model name for the embedding model — and today these are only supplied by a Docker Compose feature ("Model Runner") that won't exist in production. Without them, the app exits immediately on startup. On top of that, the database password is currently just plain text sitting in `docker-compose.yml`.

**What to do:** Two changes here:
1. Swap the code that talks to the LLM and embedding model (`internal/adapter/driven/modelclient/client.go`) to call a hosted API instead of the local model runner. While doing this, also add a timeout and a retry to the embedding call specifically — right now it has neither, which matters because it's called directly while a user is waiting on a search request.
2. Move all secrets (database password, hosted API key) out of plain text files and into a real secrets store appropriate for wherever you deploy (e.g. a Kubernetes Secret, AWS Secrets Manager, or similar).

**How you know it's done:** Start the app with only real environment variables/secrets set (no Docker Compose Model Runner involved) and confirm it boots and can successfully call the hosted LLM and embedding API.

## Step 3: Add proper logging and health checks

**What's wrong today:** Logs are currently plain text, not structured, which makes them hard to search once you have multiple machines producing them. There's also no way for a load balancer or Kubernetes to ask the app "are you healthy?" — there's no `/healthz` or `/readyz` endpoint.

**What to do:** Switch logging to output structured JSON (one consistent logging setup — right now the code mixes two slightly different logging libraries, which should be cleaned up while you're in there). Add a `/healthz` endpoint that reports whether the app can reach the database, and a `/readyz` endpoint for whether it's ready to receive traffic.

**How you know it's done:** Logs come out as JSON lines that a log viewer can filter and search. Hitting `/healthz` and `/readyz` returns a clear success/failure status. Do this now, not later — you'll want good logs and health checks while working through every step that follows.

## Step 4: Add login and separate each customer's data

**What's wrong today:** There is no login of any kind on any page or API endpoint — including the page that lets anyone view a resume file by guessing or being given its ID. Separately, even once login exists, the database has no concept of "which customer does this resume belong to." Every search, every list of resumes, and every file download currently has no filter for that at all.

**What to do:** This is the biggest step, and it has two parts that both need to happen:
1. Add a middleware that checks each incoming request has a valid login token from your identity provider, and figures out which customer ("tenant") that request belongs to.
2. Add a `tenant_id` column to the resumes table (via a database migration), and update every place that reads or writes resume data — searching, listing batches, checking status, downloading a file — to only ever look at data belonging to the logged-in customer.

Both parts matter: adding login alone stops random strangers from getting in, but without the second part, a valid logged-in customer could still see another customer's resumes.

**How you know it's done:** Write a test that logs in as "customer A" and confirms they cannot search, list, or download anything belonging to "customer B," even if they know or guess the exact ID. Confirm every page and endpoint returns "not logged in" when no token is provided at all.

## Step 5: Move resume files off local disk

**What's wrong today:** Uploaded resume files are saved to a folder on disk, shared between the app and the background worker via a Docker volume. That only works because both currently run on the same machine. Once the service runs across multiple machines, this breaks — a file saved by one machine won't be visible to another.

**What to do:** Move file storage to an object storage service (something S3-compatible). Store each file under a path that starts with the customer's tenant ID, now that Step 4 has added that concept — this way file storage is separated by customer from day one, instead of needing a second cleanup pass later.

**How you know it's done:** Upload a resume via one machine/replica and confirm a different machine/replica can retrieve and display it. Confirm each customer's files live under their own path prefix in storage.

## Step 6: Harden the web/API layer against abuse

**What's wrong today:** Three separate gaps here. First, the web server has no timeout settings, so a slow or stuck connection can hang around forever, tying up server resources. Second, there's no limit on how many requests one customer can make — a traffic spike from one customer could slow things down for everyone else sharing the service. Third, some of the plain JSON API endpoints (status, search, upload) return the raw internal error message when something goes wrong — the web pages already avoid this by showing a generic message and logging the details separately, but the JSON endpoints don't yet do the same.

**What to do:** Set read/write/idle timeouts on the web server. Add a rate limit per customer (tenant), not just a global one. Extend the same "generic message to the user, full details to the logs" pattern already used on the web pages to the JSON API endpoints too.

**How you know it's done:** Simulate a slow/stuck client and confirm the server drops the connection instead of hanging. Simulate one customer sending a burst of requests and confirm other customers are unaffected. Trigger a server error on a JSON endpoint and confirm the response no longer contains raw internal error text.

## Step 7: Deploy to a staging environment and validate

**What's wrong today:** There's no deployment target beyond a developer's laptop running Docker Compose.

**What to do:** With steps 1–6 done, the app can now start outside of Compose, has health checks, and can be trusted with real customer data. Deploy it to a staging environment on your target platform (Kubernetes or ECS) and run through the full flow: upload a resume, watch it process, search for it, view it, confirm tenant isolation holds. Only after this passes should the service be exposed to real, public traffic.

**How you know it's done:** A full upload-to-search flow works end to end in staging, across multiple replicas, with tenant isolation and health checks all verified before flipping on real traffic.

## Step 8: Tune the database for growth

**What's wrong today:** The database connection pool has no configured limits, so it just uses defaults — which can cause problems once more machines are all connecting at once. There's no index on the "status" column or the "last updated" column, both of which are used constantly by search and by the retry job — this will get slower as the number of resumes grows. The location search filter doesn't use an index that would speed it up. And the vector search used for semantic matching intentionally skipped adding a specialized index early on, on the assumption it would be revisited once resume count grew past a few thousand — that point is likely close now.

**What to do:** Set explicit, sensible limits on the database connection pool. Add the missing indexes (on status, on last-updated, and a text-search index for location). Add a vector search index (ivfflat or hnsw) for semantic search now that scale has arrived.

**How you know it's done:** Run a query plan (`EXPLAIN ANALYZE`) on search and on the retry job's query and confirm they're using indexes, not scanning the whole table. Confirm the connection pool doesn't exceed a set maximum even under load.

## Step 9: Make the resume-processing pipeline handle more volume

**What's wrong today:** Three separate issues in the background pipeline that processes uploaded resumes. First, if one resume causes an unexpected crash while being processed, it takes down the entire background worker process — there's no safety net to catch that and keep going, unlike the web server, which already has one. Second, and more important than it might sound: even though the pipeline can be split into multiple partitions, each worker still only processes one resume at a time in total, no matter how many partitions it's watching — so simply running more partitions doesn't get you more throughput. Third, today's setup only has 3 partitions and no backup copies (replication), which is fine for a demo but means losing the one server holding that data loses the queue entirely, and also caps how many workers can usefully run at once.

**What to do:** Add a safety net around each resume-processing call so one bad resume can't crash the whole worker. Change the worker so it can process several resumes at the same time within one process, using a small worker pool, instead of strictly one at a time — this is likely to help more than simply adding partitions. Set up the real production Kafka cluster with a sensible number of partitions and at least 2-3 backup copies of each one, managed the same way as the rest of your infrastructure rather than created automatically by the app.

**How you know it's done:** Force one resume to fail unexpectedly mid-processing and confirm the worker keeps processing the rest. Load-test with many resumes at once and confirm throughput actually increases with the worker pool change. Confirm the Kafka cluster survives one broker going down without losing data.

## Step 10: Add metrics and tracing

**What's wrong today:** There are currently no metrics of any kind — no dashboard, no numbers on request rate, error rate, or how long things take. There's also no single ID that follows one resume's journey from upload through every processing stage in the logs — right now you can only piece it together by the resume's own ID.

**What to do:** Add metrics (using something like Prometheus) for request counts, error counts, and processing time at each stage. Add a trace/correlation ID that gets created when a resume is uploaded and is passed through every later processing stage's logs.

**How you know it's done:** A dashboard shows live request rate, error rate, and processing time. Picking any one resume, you can find every log line about it across every processing stage using a single ID.

## Step 11: Decide on data retention and access logging

**What's wrong today:** Resumes contain personal information, and now that this is a multi-customer product, that's a real compliance question, not just an engineering one. Nothing today defines how long resume data is kept, when it should be deleted, or who is allowed to view it and when.

**What to do:** This isn't something to design in code first — raise it with whoever owns compliance/legal for a decision on retention period, deletion process, and whether access to resume data needs to be logged for audit purposes. Once that decision is made, it becomes its own follow-up piece of work.

**How you know it's done:** A written retention/deletion policy exists and is signed off by whoever owns that decision, before real customer resumes are stored in production.
