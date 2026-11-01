FROM gcr.io/distroless/base-debian13:nonroot@sha256:9039a88709bcc2bacf5a2faac331d65dd6840f763f225ad9594cc0c67bcd0fba
ARG TARGETPLATFORM
ENV GEOIP_LISTEN=":3000" GEOIP_METRICS_LISTEN=":8080"
EXPOSE 3000 8080
ENTRYPOINT [ "/usr/bin/geoip-spoa" ]
COPY $TARGETPLATFORM/geoip-spoa /usr/bin/
