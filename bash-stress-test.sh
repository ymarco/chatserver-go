#!/usr/bin/env bash
set -euo pipefail

pkill chatserver || true
PORT=4567
MSGS_COUNT=$((1<<14))

validateClientOutput() {
  NAME="$1"
  bb "(assert (str/includes? (read-line) \"Connected to\"))
      (assert (= (read-line) \"Type r to register, l to login\"))
      (assert (= (read-line) \"Username:\"))
      (assert (= (read-line) \"Password:\"))
      (assert (= (read-line) \"Logged in as $NAME\"))
      (assert (= (read-line) \"\"))
      (dotimes [i $MSGS_COUNT]
        ;(println (read-line))
        (assert (str/includes? (read-line) (str \": \" i)))
      )"
}
loginAndSendMsgs() {
  NAME="$1"
      bb "(println \"r\")
          (println \"$NAME\")
          (println \"1234\")
          (Thread/sleep 2100) ; wait for client2 to login
          (dotimes [i $MSGS_COUNT]
            (when (= (rem i 160) 0)
              (Thread/sleep 1))
            (println (str i)))
          (Thread/sleep 200); finish receiving messages"
}

client() {
  NAME="$1"
  loginAndSendMsgs $NAME\
      | go run . $PORT client\
      | validateClientOutput $NAME
}
go run . $PORT server&
serverPID=$!
sleep 0.2s

client yoav&
client1PID=$!

client nimrod&
client2PID=$!

wait $client1PID
wait $client2PID

kill $serverPID
echo "Killed server"
