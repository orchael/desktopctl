FROM nginx:1.28-alpine

COPY nginx/load-tls-env.sh /docker-entrypoint.d/05-load-tls-env.sh
RUN chmod 755 /docker-entrypoint.d/05-load-tls-env.sh
