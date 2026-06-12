import urllib.request
import struct
import sys


def listen_to_icy_stream(url):
    """
    Connects to an Icecast/SHOUTcast stream and listens for inline 'icy-metadata'
    which contains the Now Playing text (StreamTitle).
    """
    print(f"Connecting to {url}...")

    # Send the Icy-MetaData header to tell the server we want metadata interleaved
    req = urllib.request.Request(url, headers={"Icy-MetaData": "1"})

    try:
        with urllib.request.urlopen(req) as response:
            # Check if the server supports metadata
            meta_int_header = response.headers.get("icy-metaint")
            if not meta_int_header:
                print("Error: The server does not support icy-metadata.")
                return

            # The server will send `meta_int` bytes of audio, followed by a metadata chunk
            meta_int = int(meta_int_header)
            print(f"Connected! Server sends metadata every {meta_int} bytes of audio.")
            print("Listening for song changes...\n")

            while True:
                # 1. Read the audio chunk (and ignore it, or save it to a file if recording)
                audio_data = response.read(meta_int)
                if not audio_data:
                    break  # Stream ended

                # 2. Read the metadata length byte
                # The length byte tells us how many 16-byte blocks of metadata follow
                length_byte = response.read(1)
                if not length_byte:
                    break

                meta_length = struct.unpack("B", length_byte)[0] * 16

                if meta_length > 0:
                    # 3. Read the actual metadata text
                    meta_data = response.read(meta_length)
                    print(meta_data)

                    # Metadata looks like: StreamTitle='Artist - Song';StreamUrl='...';
                    # We decode it and extract the StreamTitle
                    try:
                        text = meta_data.decode("utf-8", errors="ignore").strip("\x00")
                        if "StreamTitle=" in text:
                            # Parse out the value inside the single quotes
                            start = text.find("StreamTitle='") + 13
                            end = text.find("'", start)
                            if end > start:
                                title = text[start:end]
                                if title:
                                    print(f"[NOW PLAYING] -> {title}")
                    except Exception as e:
                        print(f"Error parsing metadata: {e}")

    except KeyboardInterrupt:
        print("\nStopped listening.")
    except Exception as e:
        print(f"Failed to connect or read stream: {e}")


if __name__ == "__main__":
    # Example: A random public internet radio stream that actually sends metadata
    # (Note: The Pro FM Digi edge stream strips this out, but this is how it works on standard streams!)
    example_stream = "http://stream.radioreklama.bg:80/radio1128"
    listen_to_icy_stream(example_stream)
