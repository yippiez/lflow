import { useCallback, useEffect, useRef, useState } from "react";
import AsyncStorage from "@react-native-async-storage/async-storage";
import type { Op, Snapshot } from "./protocol";

const STORAGE_KEY = "lflow.connection";

export type Status = "disconnected" | "connecting" | "connected" | "error";

export interface Config {
  host: string; // "10.0.0.5:8765" or "ws://10.0.0.5:8765/ws"
  token: string;
}

// wsURL builds the connect URL from a host string and token. Accepts either a
// bare host:port or a full ws:// URL.
export function wsURL(host: string, token: string): string {
  let base = host.trim();
  if (!/^wss?:\/\//.test(base)) base = `ws://${base}`;
  if (!/\/ws$/.test(base.replace(/\/$/, ""))) {
    base = base.replace(/\/$/, "") + "/ws";
  }
  return `${base}?token=${encodeURIComponent(token)}`;
}

export interface Connection {
  status: Status;
  snapshot: Snapshot | null;
  config: Config | null;
  error: string | null;
  connect: (cfg: Config) => void;
  disconnect: () => void;
  sendOp: (op: Op) => void;
}

export function useConnection(): Connection {
  const [status, setStatus] = useState<Status>("disconnected");
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null);
  const [config, setConfig] = useState<Config | null>(null);
  const [error, setError] = useState<string | null>(null);

  const wsRef = useRef<WebSocket | null>(null);
  const cfgRef = useRef<Config | null>(null);
  const retryRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const wantOpenRef = useRef(false);

  const open = useCallback((cfg: Config) => {
    cfgRef.current = cfg;
    wantOpenRef.current = true;
    setStatus("connecting");
    setError(null);

    try {
      const ws = new WebSocket(wsURL(cfg.host, cfg.token));
      wsRef.current = ws;

      ws.onopen = () => setStatus("connected");
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data as string);
          if (msg.type === "snapshot") setSnapshot(msg as Snapshot);
        } catch {
          // ignore malformed frame
        }
      };
      ws.onerror = () => {
        setError("connection error");
        setStatus("error");
      };
      ws.onclose = () => {
        wsRef.current = null;
        if (wantOpenRef.current) {
          setStatus("connecting");
          retryRef.current = setTimeout(() => open(cfg), 1500);
        } else {
          setStatus("disconnected");
        }
      };
    } catch (e: any) {
      setError(String(e?.message ?? e));
      setStatus("error");
    }
  }, []);

  const connect = useCallback(
    (cfg: Config) => {
      setConfig(cfg);
      AsyncStorage.setItem(STORAGE_KEY, JSON.stringify(cfg)).catch(() => {});
      open(cfg);
    },
    [open],
  );

  const disconnect = useCallback(() => {
    wantOpenRef.current = false;
    if (retryRef.current) clearTimeout(retryRef.current);
    wsRef.current?.close();
    wsRef.current = null;
    setStatus("disconnected");
    setSnapshot(null);
    setConfig(null);
    AsyncStorage.removeItem(STORAGE_KEY).catch(() => {});
  }, []);

  const sendOp = useCallback((op: Op) => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(op));
    }
  }, []);

  // Restore a saved connection on launch and auto-connect.
  useEffect(() => {
    AsyncStorage.getItem(STORAGE_KEY)
      .then((raw) => {
        if (!raw) return;
        const cfg = JSON.parse(raw) as Config;
        setConfig(cfg);
        open(cfg);
      })
      .catch(() => {});
    return () => {
      wantOpenRef.current = false;
      if (retryRef.current) clearTimeout(retryRef.current);
      wsRef.current?.close();
    };
  }, [open]);

  return { status, snapshot, config, error, connect, disconnect, sendOp };
}
