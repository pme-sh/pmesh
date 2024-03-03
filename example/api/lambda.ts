import { WebSocket, createWebSocketStream } from "ws";

type LambdaHandler = Record<string, (body: any) => any> | ((method: string, body: any) => any);
async function newLambda(handler: LambdaHandler): Promise<string> {
	let cb: (method: string, body: any) => Promise<any>;
	if (typeof handler === "function") {
		cb = async (method, body) => {
			return await handler(method, body);
		};
	} else {
		cb = async (method, body) => {
			if (handler[method]) {
				return await handler[method](body);
			} else {
				throw new Error(`Method not found: ${method}`);
			}
		};
	}

	const duplex = createWebSocketStream(new WebSocket("ws://pm3/lambda/new"), { encoding: "utf-8" });
	return new Promise(async (resolve) => {
		for await (const message of duplex) {
			const { method, params, id } = JSON.parse(message);
			if (method === "open") {
				duplex.write(JSON.stringify({ id, result: "ok" }));
				resolve(params[0]);
				continue;
			}

			cb(method, params[0] ?? {})
				.then(
					(result) => ({ id, result }),
					(error) => ({ id, error: error.message })
				)
				.then((response) => {
					duplex.write(JSON.stringify(response));
				});
		}
	});
}

console.log("process.pid", process.pid);
process.on("SIGINT", () => {
	process.exit();
});

const id = await newLambda({
	test(body) {
		return { echo: body };
	},
});
console.log("id", id);
