import React from "react";
import { ActivityIndicator, SafeAreaView, StyleSheet, View } from "react-native";
import { StatusBar } from "expo-status-bar";
import { useFonts } from "expo-font";
import {
  JetBrainsMono_400Regular,
  JetBrainsMono_400Regular_Italic,
  JetBrainsMono_700Bold,
  JetBrainsMono_700Bold_Italic,
} from "@expo-google-fonts/jetbrains-mono";
import { colors } from "./src/theme";
import { useConnection } from "./src/connection";
import ConnectScreen from "./src/components/ConnectScreen";
import Outline from "./src/components/Outline";

export default function App() {
  const [fontsLoaded] = useFonts({
    JetBrainsMono_400Regular,
    JetBrainsMono_400Regular_Italic,
    JetBrainsMono_700Bold,
    JetBrainsMono_700Bold_Italic,
  });
  const conn = useConnection();
  const showOutline = conn.snapshot !== null && conn.config !== null;

  if (!fontsLoaded) {
    return (
      <View style={[styles.root, styles.center]}>
        <ActivityIndicator color={colors.dim} />
      </View>
    );
  }

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
  center: { alignItems: "center", justifyContent: "center" },
});
