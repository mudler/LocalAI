FROM python:3.10-bullseye
COPY . /app
WORKDIR /app
RUN pip install --no-cache-dir -r requirements.txt
ENTRYPOINT [ "python", "./functions-openai.py" ];