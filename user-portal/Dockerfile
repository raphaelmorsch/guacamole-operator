FROM registry.access.redhat.com/ubi9/nodejs-20:latest AS build
USER 0
WORKDIR /opt/app-root/src
COPY package.json ./
RUN npm install --legacy-peer-deps
COPY . .
RUN npm run build

FROM registry.access.redhat.com/ubi9/nginx-124:latest
USER 0
COPY --from=build /opt/app-root/src/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/nginx.conf
COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh && \
    chown -R 1001:0 /usr/share/nginx/html /etc/nginx/nginx.conf /docker-entrypoint.sh && \
    chmod -R g+rwX /usr/share/nginx/html
USER 1001
EXPOSE 8080
ENTRYPOINT ["/docker-entrypoint.sh"]
CMD ["nginx", "-g", "daemon off;"]
