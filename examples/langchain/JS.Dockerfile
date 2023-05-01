FROM node:latest
COPY ./langchainjs-localai-example /app
WORKDIR /app
RUN npm install
RUN npm run build
ENTRYPOINT [ "npm", "run", "start" ]