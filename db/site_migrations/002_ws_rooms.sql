-- Add room_column to ws_endpoints for channel-based WebSocket filtering.
ALTER TABLE ws_endpoints ADD COLUMN room_column TEXT DEFAULT '';
