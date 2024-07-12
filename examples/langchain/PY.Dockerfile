FROM python:3.12-bullseye
COPY ./langchainpy-localai-example /app
WORKDIR /app
RUN pip install --no-cache-dir -r requirements.txt
ENTRYPOINT [ "python", "./full_demo.py" ]
