# Keep the home CA root offline

The internal certificate authority used for the home Site's Technitium management HTTPS will keep its root private key outside Git and off the deployment Host, with a separate encrypted backup. Issuance and renewal are supervised; issued certificates and trust instructions may be versioned. This favors a smaller blast radius if the automated deployment Host is compromised, at the cost of manual certificate renewal.
