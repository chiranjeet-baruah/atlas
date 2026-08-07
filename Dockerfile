FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /app ./cmd/app

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends poppler-utils tesseract-ocr ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /app /app
COPY migrations /migrations
WORKDIR /
ENTRYPOINT ["/app"]
