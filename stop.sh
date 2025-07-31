#!/bin/bash
echo "Stopping Local MaxGPT..."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

docker-compose down

echo "✅ Local MaxGPT stopped successfully"
echo ""
echo "💡 To start again, run: ./start.sh"
