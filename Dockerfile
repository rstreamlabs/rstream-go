# See LICENSE file in the project root for license information.

FROM alpine:3

ARG BINARY

ARG TARGETPLATFORM

ENV EXEC="/usr/local/bin/${BINARY}"

RUN addgroup -S rstream && adduser -S -G rstream rstream

COPY ${TARGETPLATFORM}/release/bin/${BINARY} ${EXEC}

RUN chmod +x ${EXEC}

USER rstream

WORKDIR /home/rstream

ENTRYPOINT ["/bin/sh", "-c", "${EXEC} \"$@\"", "--"]
