FROM python:3.10

RUN python3 -m ensurepip
RUN pip3 install --no-cache --upgrade pip setuptools
RUN pip install python-dotenv tqdm langchain
RUN pip install chromadb==0.3.29 --no-deps


WORKDIR /app
RUN git clone https://github.com/go-skynet/privateGPT
WORKDIR /app/privateGPT

COPY ./privateGPT/requirements.txt .
COPY ./privateGPT/example.env ./.env

RUN pip install -r requirements.txt

#container will stay running until stopped,
# use: docker -exec privateGPT python privateFPT.py -S
CMD ["tail", "-f", "/dev/null"]
