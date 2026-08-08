SHELL := /bin/bash
BINDIR := bin
PKGS := ./...

.PHONY: help
help: ## Tampilkan daftar target
	@grep -E '^[a-zA-Z_/-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build seluruh binari ke bin/
	@mkdir -p $(BINDIR)
	go build -o $(BINDIR)/diffctl ./cmd/diffctl
	go build -o $(BINDIR)/server ./cmd/server

.PHONY: test
test: ## Jalankan seluruh test
	go test $(PKGS)

.PHONY: test-v
test-v: ## Jalankan test dengan output verbose
	go test -v $(PKGS)

.PHONY: cover
cover: ## Test dengan laporan coverage
	go test -coverprofile=coverage.out $(PKGS)
	go tool cover -func=coverage.out | tail -1

.PHONY: fmt
fmt: ## Format seluruh source
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## Gagal jika ada file yang belum diformat
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then echo "File berikut belum diformat:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## Jalankan go vet
	go vet $(PKGS)

.PHONY: tidy
tidy: ## Rapikan go.mod / go.sum
	go mod tidy

.PHONY: verify
verify: ## Verifikasi checksum dependensi
	go mod verify

.PHONY: check
check: fmt-check vet test verify ## Seluruh pemeriksaan yang dijalankan CI

.PHONY: run
run: ## Jalankan server pengembangan di :8080
	go run ./cmd/server

.PHONY: demo
demo: ## Bandingkan pasangan dokumen contoh (tanpa LLM)
	go run ./cmd/diffctl compare testdata/pair01/prev.txt testdata/pair01/curr.txt --changes

.PHONY: demo-scenarios
demo-scenarios: ## Jalankan seluruh skenario fixture lewat mesin deterministik
	@for d in testdata/scenarios/*/; do \
		name=$$(basename $$d); \
		echo "=== $$name ==="; \
		go run ./cmd/diffctl compare $$d/prev.docx $$d/curr.docx || true; \
		echo; \
	done

.PHONY: fixtures
fixtures: ## Regenerasi fixture DOCX/PDF di testdata/scenarios
	go run ./testdata/gen

.PHONY: clean
clean: ## Hapus artefak build
	rm -rf $(BINDIR) coverage.out
