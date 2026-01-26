.PHONY: vibevoice
vibevoice:
	bash install.sh

.PHONY: download-voices
download-voices:
	@echo "Downloading voice preset files..."
	@mkdir -p voices/streaming_model
	@if command -v wget >/dev/null 2>&1; then \
		wget -q -O voices/streaming_model/en-Frank_man.pt \
			https://raw.githubusercontent.com/microsoft/VibeVoice/main/demo/voices/streaming_model/en-Frank_man.pt && \
		wget -q -O voices/streaming_model/en-Grace_woman.pt \
			https://raw.githubusercontent.com/microsoft/VibeVoice/main/demo/voices/streaming_model/en-Grace_woman.pt && \
		wget -q -O voices/streaming_model/en-Mike_man.pt \
			https://raw.githubusercontent.com/microsoft/VibeVoice/main/demo/voices/streaming_model/en-Mike_man.pt && \
		wget -q -O voices/streaming_model/en-Emma_woman.pt \
			https://raw.githubusercontent.com/microsoft/VibeVoice/main/demo/voices/streaming_model/en-Emma_woman.pt && \
		wget -q -O voices/streaming_model/en-Carter_man.pt \
			https://raw.githubusercontent.com/microsoft/VibeVoice/main/demo/voices/streaming_model/en-Carter_man.pt && \
		wget -q -O voices/streaming_model/en-Davis_man.pt \
			https://raw.githubusercontent.com/microsoft/VibeVoice/main/demo/voices/streaming_model/en-Davis_man.pt && \
		echo "Voice files downloaded successfully"; \
	elif command -v curl >/dev/null 2>&1; then \
		curl -sL -o voices/streaming_model/en-Frank_man.pt \
			https://raw.githubusercontent.com/microsoft/VibeVoice/main/demo/voices/streaming_model/en-Frank_man.pt && \
		curl -sL -o voices/streaming_model/en-Grace_woman.pt \
			https://raw.githubusercontent.com/microsoft/VibeVoice/main/demo/voices/streaming_model/en-Grace_woman.pt && \
		curl -sL -o voices/streaming_model/en-Mike_man.pt \
			https://raw.githubusercontent.com/microsoft/VibeVoice/main/demo/voices/streaming_model/en-Mike_man.pt && \
		curl -sL -o voices/streaming_model/en-Emma_woman.pt \
			https://raw.githubusercontent.com/microsoft/VibeVoice/main/demo/voices/streaming_model/en-Emma_woman.pt && \
		curl -sL -o voices/streaming_model/en-Carter_man.pt \
			https://raw.githubusercontent.com/microsoft/VibeVoice/main/demo/voices/streaming_model/en-Carter_man.pt && \
		curl -sL -o voices/streaming_model/en-Davis_man.pt \
			https://raw.githubusercontent.com/microsoft/VibeVoice/main/demo/voices/streaming_model/en-Davis_man.pt && \
		echo "Voice files downloaded successfully"; \
	else \
		echo "Error: Neither wget nor curl found. Cannot download voice files."; \
		exit 1; \
	fi

.PHONY: run
run: vibevoice
	@echo "Running vibevoice..."
	bash run.sh
	@echo "vibevoice run."

.PHONY: test
test: vibevoice download-voices
	@echo "Testing vibevoice..."
	bash test.sh
	@echo "vibevoice tested."

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf venv __pycache__