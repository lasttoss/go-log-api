# Use the official Golang image to create a build artifact.
FROM golang:1.23-alpine AS builder

# Install gcc
# RUN apk add --no-cache gcc musl-dev git

RUN go install github.com/swaggo/swag/cmd/swag@latest

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download

# Copy the source from the current directory to the Working Directory inside the container
COPY . .

RUN swag init -g ./cmd/server/main.go

# Build the Go app
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main ./cmd/server

# Use the official Alpine image to create a lightweight container
FROM alpine:latest

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy the Pre-built binary file from the previous stage
COPY --from=builder /app/main /app/main

# Copy the .env file
#COPY .env .
COPY config.yaml .

# Command to run the executable
CMD ["/app/main"]