FROM gcr.io/distroless/static-debian12:nonroot

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/intropy /usr/local/bin/intropy

# Scaffolding commands write into the working directory; mount your project
# here and match your uid so files aren't owned by 65532:
#   docker run --rm -v "$PWD:/work" --user "$(id -u):$(id -g)" \
#     ghcr.io/integrio-intropy/intropy-cli ...
WORKDIR /work

ENTRYPOINT ["/usr/local/bin/intropy"]
CMD ["--help"]
