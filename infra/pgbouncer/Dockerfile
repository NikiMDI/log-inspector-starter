FROM alpine:3.22
RUN apk add --no-cache pgbouncer gettext postgresql17-client && \
    addgroup -S pgbouncer && adduser -S -D -H -G pgbouncer pgbouncer
COPY pgbouncer.ini.template /etc/pgbouncer/pgbouncer.ini.template
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh && chown -R pgbouncer:pgbouncer /etc/pgbouncer
USER pgbouncer
EXPOSE 6432
ENTRYPOINT ["/entrypoint.sh"]
