package tesco

// GraphQL operations, ported from observed tesco.com traffic and verified
// against the live endpoint. Each is tagged with the micro-frontend name
// Tesco's own client sends in extensions.mfeName; the gateway routes on it.

const (
	mfeSearch     = "mfe-plp"
	mfeProduct    = "mfe-pdp"
	mfeBasket     = "mfe-basket"
	mfeSlots      = "mfe-slots"
	mfeOrders     = "mfe-orders"
	mfeFavourites = "mfe-favourites"
)

// productFields is the selection shared by search and category results. Kept
// narrow: every field here is one Tesco can rename out from under us.
const productFields = `
  tpnc
  tpnb
  title
  brandName
  defaultImageUrl
  isForSale
  price { actual unitPrice unitOfMeasure }
  promotions {
    description
    startDate
    endDate
    attributes
    price { afterDiscount beforeDiscount }
  }
`

const searchQuery = `query Search($query: String!, $page: Int = 1, $count: Int) {
  search(query: $query, page: $page, count: $count) {
    info { total count page offset }
    results {
      node {
        __typename
        ... on ProductType {` + productFields + `}
      }
    }
  }
}`

const categoryQuery = `query GetCategoryProducts($facet: ID, $page: Int = 1, $count: Int) {
  category(facet: $facet, page: $page, count: $count) {
    info { total count page offset }
    results {
      node {
        __typename
        ... on ProductType {` + productFields + `}
      }
    }
  }
}`

const productQuery = `query GetProduct($tpnc: String!) {
  product(tpnc: $tpnc) {` + productFields + `
    catchWeightList { price weight default }
    details {
      packSize { value units }
      nutrition { name value1 value2 value3 }
      ingredients
    }
  }
}`

const taxonomyQuery = `query Taxonomy {
  taxonomy(includeInspirationEvents: false) {
    id
    name
    label
    children {
      id
      name
      label
    }
  }
}`

// Authenticated operations. Their shapes were validated against the live
// gateway without a session: it runs GraphQL validation before auth, so a
// misspelt field answers "Cannot query field X on type Y" while a well-formed
// one answers "Unauthorized". That confirms the selections are valid; it does
// not confirm the runtime data, which needs a real account.

// basketItemFields is shared by the basket query and mutation so a write
// returns the same fidelity as a read.
const basketItemFields = `
  id
  quantity
  cost
  unit
  weight
  product {
    id
    tpnc
    tpnb
    title
    brandName
    defaultImageUrl
    isForSale
    price { actual unitPrice unitOfMeasure }
  }
`

const basketQuery = `query GetBasket {
  basket {
    id
    guidePrice
    isInAmend
    amendExpiry
    shoppingMethod
    items {` + basketItemFields + `}
  }
}`

const updateBasketQuery = `mutation UpdateBasket($items: [BasketLineItemInputType], $orderId: ID) {
  basket(items: $items, orderId: $orderId) {
    id
    guidePrice
    isInAmend
    amendExpiry
    shoppingMethod
    items {` + basketItemFields + `}
  }
}`

const favouritesQuery = `query GetFavourites($page: Int = 1, $count: Int) {
  favourites(page: $page, count: $count) {
    info { total count page offset }
    products {
      __typename
      ... on ProductType {` + productFields + `}
    }
  }
}`

const ordersQuery = `query GetPreviousOrdersWithPagination($orderContexts: [OrderContextType], $page: Int, $count: Int) {
  orderSearch(page: $page, count: $count, orderContexts: $orderContexts) {
    orders {
      id
      orderNo
      status
      createdDateTime
      totalPrice
      slot { start end charge }
    }
  }
}`

const orderQuery = `query GetOrderReceipt($id: ID!) {
  order(orderId: $id) {
    id
    orderNo
    status
    createdDateTime
    totalPrice
    slot { start end charge }
    items {
      quantity
      cost
      unit
      weight
      product { tpnc tpnb title brandName }
    }
  }
}`

const slotsQuery = `query DeliverySlots($start: String, $end: String) {
  delivery(start: $start, end: $end) {
    id
    start
    end
    charge
    status
    group
    price { beforeDiscount afterDiscount }
    locationUuid
  }
}`
