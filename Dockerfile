# syntax=docker/dockerfile:1

# --- build stage ---------------------------------------------------------------
FROM golang:1.26 AS build
WORKDIR /src

# Cache module downloads separately from the source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Static, stripped binary so it runs on a distroless/scratch base. SQL migrations
# are embedded via go:embed, so no extra files need copying into the final image.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/server ./cmd/server

# --- runtime stage -------------------------------------------------------------
# distroless/static:nonroot has no shell or package manager (small, hardened) and
# already runs as an unprivileged user.
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/server /server
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/server"]
