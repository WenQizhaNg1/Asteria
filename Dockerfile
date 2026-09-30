FROM golang:1.27.1 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -o /asteria ./cmd/asteria

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 asteria \
    && useradd --uid 10001 --gid asteria --no-create-home asteria \
    && mkdir /data && chown asteria:asteria /data
COPY --from=build /asteria /usr/local/bin/asteria
USER 10001:10001
ENV ASTERIA_DATA=/data
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["asteria"]
CMD ["serve", "--addr", "0.0.0.0:8080"]
