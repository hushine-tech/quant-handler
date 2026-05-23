BIN=bin/quant-handler
CONFIG?=./config.yaml
PID_FILE=.run.pid

.PHONY: build dev start stop clean test tidy

tidy:
	go mod tidy

test:
	go test ./...

build:
	mkdir -p bin
	go build -o $(BIN) ./cmd/quant-handler

dev:
	go run ./cmd/quant-handler -config $(CONFIG)

start: build
	mkdir -p logs
	python3 -c 'import subprocess; out=open("logs/quant-handler.out","ab",buffering=0); p=subprocess.Popen(["./$(BIN)","-config","$(CONFIG)"], stdout=out, stderr=subprocess.STDOUT, start_new_session=True, close_fds=True); open("$(PID_FILE)","w").write(str(p.pid)+"\n")'
	@echo "✓ quant-handler started (pid=$$(cat $(PID_FILE))), logs at gateway/quant-handler/logs/quant-handler.out"

stop:
	@if [ -f $(PID_FILE) ]; then kill $$(cat $(PID_FILE)) 2>/dev/null || true; rm -f $(PID_FILE); echo "✓ quant-handler stopped"; else echo "(no $(PID_FILE), nothing to stop)"; fi

clean:
	rm -rf bin $(PID_FILE)
