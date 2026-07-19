FROM python:3.12-slim

WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY app.py .

ENV BROWSER_WORKER_ADDR=0.0.0.0:8192
EXPOSE 8192

CMD ["python", "app.py"]
