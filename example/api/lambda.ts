import { WebSocket, createWebSocketStream } from "ws";

type LambdaHandler = Record<string, (body: any) => any>;
async function newLambda(handler: LambdaHandler): Promise<string> {
	const duplex = createWebSocketStream(new WebSocket("ws://pm3/lambda/new"), { encoding: "utf-8" });

	async function handle(id: any, method: string, body: any) {
		let resp;
		try {
			const cb = handler[method];
			if (!cb) {
				throw new Error(`method not found: ${method}`);
			}
			const result = await cb(body);
			resp = { id, result };
		} catch (error) {
			resp = { id, error: `${error}` };
		}
		console.log("->", resp);
		duplex.write(JSON.stringify(resp));
	}

	return new Promise(async (resolve) => {
		for await (const message of duplex) {
			console.log("<-", message);
			const { method, params, id } = JSON.parse(message);
			if (method === "open") {
				console.log("->", { id, result: "ok" });
				duplex.write(JSON.stringify({ id, result: "ok" }));
				resolve(params[0]);
				continue;
			}
			handle(id, method, params[0]);
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
