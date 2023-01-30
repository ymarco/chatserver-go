#!/usr/bin/env bash
set -euo pipefail

pkill chatserver || true
PORT=4567
MSGS_COUNT=$((1<<14))

client1() {
    echo $MSGS_COUNT\
      | bb '(println "r")
            (println "yoav")
            (println "1234")
            (Thread/sleep 2100) ; wait for client2 to login
            (dotimes [i (read)]
              (when (= (rem i 320) 0)
                (Thread/sleep 1))
              (println "msg"))
            (Thread/sleep 200); finish receiving messages' \
      | go run . $PORT client\
      | bb '(read-line) (read-line) ; connected, type r or l
            (read-line) ; "Username:"
            (read-line) ; "Password:"
            (read-line) ; "Logged in as..."
            (read-line) ; ""
            (println "client1: received " (count (line-seq (java.io.BufferedReader. *in*))))'
}
client2() {
    echo $MSGS_COUNT\
      | bb '(println "r")
            (println "nimrod")
            (println "1234")
            (Thread/sleep 2000) ; wait for client2 to login
            (dotimes [i (read)]
              (when (= (rem i 320) 0)
                (Thread/sleep 1))
              (println "msg"))
            (Thread/sleep 200); finish receiving messages' \
      | go run . $PORT client \
      | bb '(read-line) (read-line) ; connected, type r or l
            (read-line) ; "Username:"
            (read-line) ; "Password:"
            (read-line) ; "Logged in as..."
            (read-line) ; ""
            (println "client2: received " (count (line-seq (java.io.BufferedReader. *in*))))'
}
go run . $PORT server&
serverPID=$!
sleep 0.2s

client1&
client1PID=$!

client2&
client2PID=$!

wait $client1PID
wait $client2PID

kill -INT $serverPID
echo "Killed server"
