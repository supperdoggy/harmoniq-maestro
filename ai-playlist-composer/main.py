import json
import random
import time
import requests
from pymongo import MongoClient

# === CONFIG ===
MONGO_URI = "mongodb://root:SssnI5NMth5OeedbPmbQ49DxEbT726@100.111.149.52:27017/"
DB_NAME = "music-services"
COLLECTION_NAME = "music-files"

OLLAMA_URL = "http://100.79.119.60:11434/api/generate"
OLLAMA_MODEL = "gemma:2b"

BATCH_SIZE = 300
MAX_RETRIES = 3
PLAYLIST_ITEMS_PER_BATCH = 5  # smaller chunks to merge later
TOTAL_TARGET_ITEMS = 50       # optional cap


def get_all_songs():
    client = MongoClient(MONGO_URI)
    db = client[DB_NAME]
    collection = db[COLLECTION_NAME]

    # Exclude both _id and meta_data fields
    cursor = collection.find({}, {"_id": 0, "meta_data": 0})

    all_songs = []
    for doc in cursor:
        try:
            all_songs.append(doc)
        except Exception as e:
            print(f"⚠️ Skipping bad doc: {e}")
    return all_songs


def ask_ollama_with_retry(songs_batch, playlist_request, retries=MAX_RETRIES):
    messages = [
        {
            "role": "system",
            "content": "You are a music expert and playlist curator."
        },
        {
            "role": "user",
            "content": f"""Here is a list of songs in JSON format:

{json.dumps(songs_batch, indent=2)}

Pick {PLAYLIST_ITEMS_PER_BATCH} songs from this list for the theme:
"{playlist_request}"

Only pick from songs I gave you. Return just a JSON array like:
[
  {{ "title": "Song Title", "artist": "Artist" }},
  ...
]
"""
        }
    ]

    data = {
        "model": OLLAMA_MODEL,
        "stream": False,
        "messages": messages
    }

    attempt = 0
    while attempt < retries:
        try:
            print(f"📤 Sending batch of {len(songs_batch)} songs (attempt {attempt + 1})...")
            response = requests.post(OLLAMA_URL, json=data, timeout=120)
            response.raise_for_status()

            resp_json = response.json()

            # Chat models like llama3, phi, gemma
            if "message" in resp_json and "content" in resp_json["message"]:
                return resp_json["message"]["content"]

            # Simpler models like tinyllama or Mistral
            elif "response" in resp_json:
                return resp_json["response"]

            else:
                print(f"⚠️ Unexpected Ollama response: {resp_json}")
                return None

        except Exception as e:
            print(f"⚠️ Error: {e}")
            attempt += 1
            time.sleep(2 ** attempt)
    return None



def parse_playlist(raw_response):
    try:
        return json.loads(raw_response)
    except json.JSONDecodeError:
        return []


def collect_full_playlist(all_songs, playlist_request):
    full_playlist = []
    seen_titles = set()

    random.shuffle(all_songs)
    for i in range(0, len(all_songs), BATCH_SIZE):
        batch = all_songs[i:i + BATCH_SIZE]
        raw = ask_ollama_with_retry(batch, playlist_request)
        if not raw:
            continue
        partial = parse_playlist(raw)

        for song in partial:
            key = (song.get("title", "").strip().lower(), song.get("artist", "").strip().lower())
            if key not in seen_titles:
                full_playlist.append(song)
                seen_titles.add(key)

        if len(full_playlist) >= TOTAL_TARGET_ITEMS:
            break

    return full_playlist


def main():
    playlist_request = "Energetic highschool rock anthems from the 80s and 90s, with a focus on guitar solos and catchy choruses."

    print("🎼 Loading all songs from MongoDB...")
    all_songs = get_all_songs()
    print(f"🎶 Total songs: {len(all_songs)}")

    playlist = collect_full_playlist(all_songs, playlist_request)

    if playlist:
        print(f"\n✅ Final Playlist ({len(playlist)} songs):")
        for i, song in enumerate(playlist, start=1):
            print(f"{i}. {song['title']} – {song['artist']}")
    else:
        print("❌ Failed to generate a playlist.")

    # Optional: save
    with open("final_playlist.json", "w", encoding="utf-8") as f:
        json.dump(playlist, f, indent=2)


if __name__ == "__main__":
    main()
