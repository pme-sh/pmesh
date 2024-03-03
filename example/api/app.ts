import Fastify from "fastify";

// Create the server.
//
const server = Fastify({ logger: false, trustProxy: true });
server.register(import("./hello.js"));

// Start the server.
//
const host = process.env.HOST || "127.0.0.1";
if (process.env.SOCKET_PATH) {
	server.listen({ host, path: process.env.SOCKET_PATH });
} else {
	server.listen({ host, port: process.env.PORT || 3000 });
}
console.log(`Server listening on ${host}:${process.env.PORT || process.env.SOCKET_PATH}`);

async function test() {
	const pong = await (await fetch(`http://pm3/ping`)).json();
	console.log(pong);
}

test();
