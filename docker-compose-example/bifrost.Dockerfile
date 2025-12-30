# Dockerfile for bifrost to be used in docker-compose example
FROM golang:1.25-alpine

WORKDIR /app/bifrost

# Copy go.mod and go.sum files for bifrost
COPY go.mod go.sum ./

# Download all dependencies.
RUN go mod download

# Copy the source code for bifrost
COPY . .

# Build the Go app
RUN go build -o /bifrost-proxy ./cmd/main.go

# Expose port 8080 to the outside world
EXPOSE 8080

# Set the entrypoint
ENTRYPOINT ["/bifrost-proxy"]
