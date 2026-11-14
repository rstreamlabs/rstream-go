# See LICENSE file in the project root for license information.

FROM alpine:3@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf

LABEL io.modelcontextprotocol.server.name="io.github.rstreamlabs/rstream"

ARG BINARY

ARG TARGETPLATFORM

ENV EXEC="/usr/local/bin/${BINARY}"

RUN addgroup -S rstream && adduser -S -G rstream rstream

COPY ${TARGETPLATFORM}/release/bin/${BINARY} ${EXEC}

RUN chmod +x ${EXEC}

USER rstream

WORKDIR /home/rstream

ENTRYPOINT ["/bin/sh", "-c", "${EXEC} \"$@\"", "--"]
