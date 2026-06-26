import React, { useState } from "react";
import {
  KeyboardAvoidingView,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { colors, font } from "../theme";
import type { Config, Status } from "../connection";

interface Props {
  status: Status;
  error: string | null;
  initial: Config | null;
  onConnect: (cfg: Config) => void;
}

export default function ConnectScreen({ status, error, initial, onConnect }: Props) {
  const [host, setHost] = useState(initial?.host ?? "");
  const [token, setToken] = useState(initial?.token ?? "");

  const canConnect = host.trim() !== "" && token.trim() !== "";
  const connecting = status === "connecting";

  return (
    <KeyboardAvoidingView
      style={styles.wrap}
      behavior={Platform.OS === "ios" ? "padding" : undefined}
    >
      <View style={styles.card}>
        <Text style={styles.title}>lflow</Text>
        <Text style={styles.subtitle}>connect to a running `lflow serve`</Text>

        <Text style={styles.label}>server</Text>
        <TextInput
          style={styles.input}
          placeholder="10.0.0.5:8765"
          placeholderTextColor={colors.dim}
          autoCapitalize="none"
          autoCorrect={false}
          value={host}
          onChangeText={setHost}
        />

        <Text style={styles.label}>token</Text>
        <TextInput
          style={styles.input}
          placeholder="paste the token from the terminal"
          placeholderTextColor={colors.dim}
          autoCapitalize="none"
          autoCorrect={false}
          value={token}
          onChangeText={setToken}
        />

        <Pressable
          style={[styles.button, !canConnect && styles.buttonDisabled]}
          disabled={!canConnect || connecting}
          onPress={() => onConnect({ host: host.trim(), token: token.trim() })}
        >
          <Text style={styles.buttonText}>
            {connecting ? "connecting…" : "connect"}
          </Text>
        </Pressable>

        {error && status === "error" ? (
          <Text style={styles.error}>{error}</Text>
        ) : null}
      </View>
    </KeyboardAvoidingView>
  );
}

const styles = StyleSheet.create({
  wrap: {
    flex: 1,
    backgroundColor: colors.bg,
    justifyContent: "center",
    padding: 24,
  },
  card: { maxWidth: 420, width: "100%", alignSelf: "center" },
  title: { color: colors.fg, fontFamily: font.bold, fontSize: 28 },
  subtitle: { color: colors.dim, fontFamily: font.regular, fontSize: 13, marginBottom: 24 },
  label: {
    color: colors.dim,
    fontFamily: font.regular,
    fontSize: 12,
    marginTop: 14,
    marginBottom: 6,
  },
  input: {
    backgroundColor: colors.bgCode,
    color: colors.fg,
    fontFamily: font.regular,
    fontSize: 15,
    borderRadius: 6,
    paddingHorizontal: 12,
    paddingVertical: 10,
    borderWidth: 1,
    borderColor: colors.border,
  },
  button: {
    marginTop: 24,
    backgroundColor: colors.accent,
    borderRadius: 6,
    paddingVertical: 12,
    alignItems: "center",
  },
  buttonDisabled: { opacity: 0.4 },
  buttonText: { color: "#fff", fontFamily: font.bold, fontSize: 15 },
  error: { color: colors.red, fontFamily: font.regular, fontSize: 13, marginTop: 14 },
});
