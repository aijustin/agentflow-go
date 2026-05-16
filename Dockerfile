FROM cgr.dev/chainguard/go:latest-dev AS build

ARG TARGET=agent-http
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/agentflow ./cmd/${TARGET}

FROM cgr.dev/chainguard/static:latest

USER 65532:65532
COPY --from=build /out/agentflow /usr/local/bin/agentflow
EXPOSE 18080
ENTRYPOINT ["/usr/local/bin/agentflow"]
