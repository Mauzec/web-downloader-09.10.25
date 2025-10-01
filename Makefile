start:
	go run cmd/server/main.go

rmdata:
	@read -p "Are you sure? This will delete ALL data. Enter 'yes' to continue: " confirm && [ "$$confirm" = "yes" ] && \
	rm -rf $(SEC_DIR) && \
	rm -rf $(FILES_DIR) && \
	echo "All data deleted."

playground:
	go run cmd/playground/main.go

test:
	@if command -v gotestsum > /dev/null; then \
		gotestsum --debug --format testname; \
	else \
		go test ./...; \
	fi

testv:
	@if command -v gotestsum > /dev/null; then \
		gotestsum --debug --format standard-verbose; \
	else \
		go test -v ./...; \
	fi

.PHONY: start, playground, rmdata, test, testv