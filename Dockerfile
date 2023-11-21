FROM gcr.io/distroless/static-debian11:nonroot
ENTRYPOINT ["/baton-discord"]
COPY baton-discord /