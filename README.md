# Go.Kitchen - Backend MVP

Backend service for a food delivery platform. This project provides a robust, concurrent-safe API for customers to explore menus and place orders, and a closed partner API for establishments to manage their catalogs and process incoming orders.


## Tech Stack
- **Language**: Go 1.26
- **Database**: PostgreSQL 17
- **Routing**: `go-chi/v5`
- **Database Driver**: `pgx/v5` (Pure SQL, no ORM)
- **Infrastructure**: Docker & Docker Compose

## Architecture & Technical Highlights
The project follows a **Layered Architecture** (Handler -> Service -> Repository) focusing on domain-driven design and transactional integrity under high concurrency.

- **Data Integrity & Concurrency**: Hand-written SQL queries using `pgx` for optimal performance. Explicit `FOR UPDATE` locking mechanisms ensure no overselling of inventory during concurrent checkout spikes.
- **UUID v7**: Used for primary keys to ensure time-ordered database insertions, preventing B-Tree index fragmentation and optimizing read/write performance.
- **Advanced PostgreSQL Features**: Uses `pg_trgm` (trigram indexes) for ultra-fast full-text search capabilities across menus and establishments.
- **Manual Dependency Injection**: Simple and explicit dependency wiring in `cmd/kitchen-service/main.go` without reflection-based DI frameworks.
- **Robust Integration Testing**: Comprehensive test suite running against a real PostgreSQL instance in Docker, validating ACID properties and race conditions.

## Quick Start

The project is fully dockerized. To start the application and the database:

```bash
docker compose up --build
```

This will start:
- PostgreSQL 17 on port `5432` (schema migrations are applied automatically).
- Kitchen API Service on port `8080`.
- Demo Partner Restaurant Emulator on port `8081` (a mock client to simulate partner activity).

*The database is automatically seeded with demo data (a pizzeria with a menu) so you can test the API immediately.*

Example request to fetch active restaurants:
```bash
curl -s http://localhost:8080/api/v1/restaurants
```

## Testing

```bash
make test              # Run unit and service tests
make test-integration  # Run integration tests against a real Postgres instance
make lint              # Run golangci-lint
make cover             # Check test coverage
```

## Documentation
- **OpenAPI 3.0**: The API contract is fully documented in `api/openapi.yaml`.
- **System Design**: C4 model diagrams and Customer Journey Maps (CJM) are available in the `docs/diagrams/` directory.
- **Performance Benchmarks**: SQL scripts used to benchmark the database schema (50k restaurants, 200k menu items) are available in `docs/benchmark.sql`.
