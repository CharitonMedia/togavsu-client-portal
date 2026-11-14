# Client Portal module image.
# Build from the repository root: docker build -t togavsu-client-portal .
FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/client-portal ./cmd/client-portal

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/client-portal /client-portal
EXPOSE 8080
ENTRYPOINT ["/client-portal", "serve"]
