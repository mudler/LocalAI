FROM python:3.10

RUN python3 -m ensurepip
RUN pip3 install --no-cache --upgrade pip setuptools
RUN pip install python-dotenv tqdm langchain
RUN pip install chromadb==0.3.29 --no-deps

COPY ./entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

WORKDIR /app

#RUN git clone https://github.com/go-skynet/privateGPT

WORKDIR /app/privateGPT


COPY ./privateGPT/requirements.txt .

RUN pip install -r requirements.txt



ENTRYPOINT ["/app/entrypoint.sh"]
#CMD ["tail", "-f", "/dev/null"]
