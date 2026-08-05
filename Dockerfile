# See LICENSE file in the project root for license information.

FROM alpine:3@sha256:5b10f432ef3da1b8d4c7eb6c487f2f5a8f096bc91145e68878dd4a5019afde11

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
