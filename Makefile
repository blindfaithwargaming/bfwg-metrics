IMAGE ?= bfwg-metrics:dev
KIND_CLUSTER ?= bfwg

.PHONY: test lint run etl-sample docker up down kind-up kind-deploy kind-down

test:
	go test -race ./...

lint:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	go vet ./...

etl-sample:
	go run ./cmd/etl -games testdata/club_night_games.json -command-logs testdata/command_logs.json

run: etl-sample
	go run ./cmd/server

docker:
	docker build -t $(IMAGE) .

up:
	docker compose up --build

down:
	docker compose down -v

kind-up:
	kind create cluster --name $(KIND_CLUSTER)

kind-deploy: docker
	kind load docker-image $(IMAGE) --name $(KIND_CLUSTER)
	kubectl apply -k deploy/k8s
	kubectl -n bfwg-metrics delete job etl-initial --ignore-not-found
	kubectl -n bfwg-metrics create job etl-initial --from=cronjob/bfwg-metrics-etl
	kubectl -n bfwg-metrics rollout status deployment/bfwg-metrics

kind-down:
	kind delete cluster --name $(KIND_CLUSTER)
