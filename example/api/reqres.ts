import * as Nats from "nats";

const nc = await Nats.connect();
console.log("Connected to " + nc.getServer());

async function handle(handler: string) {
	const sub = nc.subscribe("foo", { queue: "bar" });
	for await (const msg of sub) {
		console.log(handler, "Received a message: " + msg.data);
		msg.respond(handler + "bar");
	}
}
handle("h-a");
handle("h-b");

const result = await nc.requestMany("foo", "hi");
for await (const msg of result) {
	console.log(msg.data);
}
const one = await nc.request("foo", "hi");
console.log(one.data);
