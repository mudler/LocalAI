#!/bin/bash
echo "Starting Local MaxGPT..."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  🤖 Local MaxGPT - BETA"
echo "  Running on: http://localhost:8080"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

if ! command -v nvidia-smi &> /dev/null; then
    echo "⚠️  Warning: NVIDIA GPU not detected"
    echo "   Local MaxGPT will run on CPU mode"
else
    echo "✅ NVIDIA GPU detected"
    nvidia-smi --query-gpu=name,memory.total --format=csv,noheader,nounits | head -1
fi

mkdir -p data

echo ""
echo "🚀 Starting Local MaxGPT services..."

docker-compose up -d

echo "⏳ Waiting for Local MaxGPT to initialize..."
sleep 10

if docker-compose ps | grep -q "local-maxgpt.*Up"; then
    echo "✅ Local MaxGPT is running!"
    echo ""
    echo "🌐 Access Local MaxGPT:"
    echo "   Web UI: http://localhost:8080"
    echo "   API:    http://localhost:8080/v1"
    echo ""
    
    if command -v xdg-open &> /dev/null; then
        echo "🔗 Opening browser..."
        xdg-open http://localhost:8080
    elif command -v open &> /dev/null; then
        echo "🔗 Opening browser..."
        open http://localhost:8080
    else
        echo "💡 Open http://localhost:8080 in your browser"
    fi
else
    echo "❌ Failed to start Local MaxGPT"
    echo "📋 Check logs with: docker-compose logs"
fi
