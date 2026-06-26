import React from "react";
import { SafeAreaView, StyleSheet } from "react-native";
import { StatusBar } from "expo-status-bar";
import { colors } from "./src/theme";
import { useConnection } from "./src/connection";
import ConnectScreen from "./src/components/ConnectScreen";
import Outline from "./src/components/Outline";

export default function App() {
  const conn = useConnection();
  const showOutline = conn.snapshot !== null && conn.config !== null;

  return (
    <SafeAreaView style={styles.root}>
      <StatusBar style="light" />
      {showOutline ? (
        <Outline
          snapshot={conn.snapshot!}
          connected={conn.status === "connected"}
          onDisconnect={conn.disconnect}
          sendOp={conn.sendOp}
        />
      ) : (
        <ConnectScreen
          status={conn.status}
          error={conn.error}
          initial={conn.config}
          onConnect={conn.connect}
        />
      )}
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1, backgroundColor: colors.bg },
});
