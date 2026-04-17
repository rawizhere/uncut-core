#!/bin/bash
# Noise Generator Script

ORIGINS=(
    "https://cloud.docker.com/"
    "https://swscan.apple.com/"
    "https://api.github.com/meta"
    "https://s3.amazonaws.com/"
)

# Pick a random origin
URL=${ORIGINS[$RANDOM % ${#ORIGINS[@]}]}

RANGE_START=$((RANDOM % 5000))
RANGE_SIZE=$(( (RANDOM % 200000) + 50000 )) # 50K - 250K bytes
RANGE_END=$((RANGE_START + RANGE_SIZE))

# Execute noise request with rate limit to keep it subtle
echo "Noise Generator: Requesting $URL (Range: $RANGE_START-$RANGE_END)"
curl -s -o /dev/null -H "Range: bytes=$RANGE_START-$RANGE_END" --limit-rate 500K "$URL"
