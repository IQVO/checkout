import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/checkout-api",
    },
    {
      type: "category",
      label: "CheckoutSessions",
      link: {
        type: "doc",
        id: "api-reference/rest/checkout-sessions",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/open-checkout-session",
          label: "Open a checkout session for a cart",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-checkout-session",
          label: "Get a checkout session",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/confirm-checkout-session",
          label: "Confirm the session with a payment method token",
          className: "api-method post",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
