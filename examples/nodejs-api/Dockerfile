FROM node:20-alpine
WORKDIR /app
COPY package.json ./
RUN npm install --omit=dev
COPY server.js domain.js obsynx.yaml ./
EXPOSE 8082
CMD ["node", "server.js"]
